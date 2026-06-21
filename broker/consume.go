package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/gmb-sig/go-platform-kit/observability"
)

// Handler processes a decoded event. The broker is assumed at-least-once
// delivery and Dispatch marks an event processed only AFTER the handler
// succeeds, so a handler may be invoked more than once for the same event id
// (e.g. when a redelivery races a slow first attempt across replicas, or when
// recording the processed mark fails). Handlers must therefore be idempotent —
// for audit consumers, an INSERT keyed on event_id (ON CONFLICT DO NOTHING) is
// the natural shape.
type Handler func(ctx context.Context, ev *Envelope) error

// IdempotencyStore tracks which event ids have completed processing, so a
// redelivered message is not handled twice (at-least-once delivery is
// assumed). Seen is a read-only check; MarkProcessed records the id only after
// the handler has succeeded — never before, so a handler failure can never
// cause an event to be acknowledged unprocessed. Implementations must be safe
// for concurrent use; a multi-replica consumer needs a shared store (e.g.
// Redis) for the dedup to hold across replicas.
type IdempotencyStore interface {
	// Seen reports whether eventID has already been successfully processed.
	Seen(ctx context.Context, eventID string) (bool, error)
	// MarkProcessed durably records eventID as processed. Called by Dispatch
	// only after the handler returned nil.
	MarkProcessed(ctx context.Context, eventID string) error
}

// Dispatch decodes a raw broker payload into an envelope and invokes handler,
// deduplicating redeliveries via store. A payload that fails to decode or
// validate is rejected without invoking the handler.
//
// Delivery semantics: **at-least-once handler invocation, never-lost events.**
// The event id is marked processed only after the handler succeeds; if the
// handler — or recording the mark — fails, Dispatch returns the error so the
// caller nacks and the broker redelivers. The trade-off is that a redelivery
// can invoke the handler again (see Handler); the alternative (mark-before-
// handle) silently drops an event whose first handling failed, which is
// unacceptable for an audit trail.
//
// It returns nil when the message is a duplicate (already processed) so the
// caller can safely acknowledge it.
func Dispatch(ctx context.Context, payload []byte, store IdempotencyStore, handler Handler) error {
	if handler == nil {
		return errors.New("broker: nil handler")
	}

	ev := &Envelope{}
	if err := json.Unmarshal(payload, ev); err != nil {
		incConsume(outcomeInvalid)

		return fmt.Errorf("broker: decode envelope: %w", err)
	}

	if err := ev.Validate(); err != nil {
		incConsume(outcomeInvalid)

		return err
	}

	if store != nil {
		seen, err := store.Seen(ctx, ev.EventID)
		if err != nil {
			incConsume(outcomeError)

			return fmt.Errorf("broker: idempotency check for %q: %w", ev.EventID, err)
		}

		if seen {
			// Already processed — safe to acknowledge.
			incConsume(outcomeDuplicate)

			return nil
		}
	}

	if err := handler(ctx, ev); err != nil {
		incConsume(outcomeError)

		return err
	}

	if store != nil {
		if err := store.MarkProcessed(ctx, ev.EventID); err != nil {
			// The event WAS processed; failing here causes a redelivery and a
			// second handler invocation — acceptable (handlers are idempotent),
			// whereas swallowing the error would leave the dedup mark missing
			// silently.
			incConsume(outcomeError)

			return fmt.Errorf("broker: mark processed %q: %w", ev.EventID, err)
		}
	}

	incConsume(observability.OutcomeSuccess)

	return nil
}

// Consume-outcome metric label values (in addition to the standard success).
const (
	outcomeError     = observability.OutcomeError
	outcomeDuplicate = "duplicate"
	outcomeInvalid   = "invalid"
)

func incConsume(outcome string) {
	observability.IncCounter(observability.MetricBrokerConsumeTotal, map[string]string{
		observability.LabelOutcome: outcome,
	})
}

// DefaultIdempotencyCapacity bounds the in-memory store: when full, the oldest
// recorded ids are evicted first (FIFO). Size it above the broker's redelivery
// window for the consumer's throughput.
const DefaultIdempotencyCapacity = 65536

// MemoryIdempotencyStore is an in-process, capacity-bounded IdempotencyStore.
// It is suitable for tests and single-instance consumers; multi-instance
// consumers should back the store with shared state (e.g. Redis SETNX) so dedup
// holds across replicas. Eviction is FIFO — an id older than the retained
// window is treated as unseen again, which is safe given idempotent handlers.
type MemoryIdempotencyStore struct {
	mu       sync.Mutex
	seen     map[string]struct{}
	order    []string
	capacity int
}

// NewMemoryIdempotencyStore returns an empty in-memory store bounded at
// DefaultIdempotencyCapacity.
func NewMemoryIdempotencyStore() *MemoryIdempotencyStore {
	return NewMemoryIdempotencyStoreWithCapacity(DefaultIdempotencyCapacity)
}

// NewMemoryIdempotencyStoreWithCapacity returns an empty in-memory store
// retaining at most capacity ids (DefaultIdempotencyCapacity when capacity <= 0).
func NewMemoryIdempotencyStoreWithCapacity(capacity int) *MemoryIdempotencyStore {
	if capacity <= 0 {
		capacity = DefaultIdempotencyCapacity
	}

	return &MemoryIdempotencyStore{
		seen:     make(map[string]struct{}),
		capacity: capacity,
	}
}

// Seen reports whether eventID has been marked processed.
func (s *MemoryIdempotencyStore) Seen(_ context.Context, eventID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok := s.seen[eventID]

	return ok, nil
}

// MarkProcessed records eventID, evicting the oldest id when at capacity.
func (s *MemoryIdempotencyStore) MarkProcessed(_ context.Context, eventID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.seen[eventID]; ok {
		return nil
	}

	for len(s.order) >= s.capacity {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.seen, oldest)
	}

	s.seen[eventID] = struct{}{}
	s.order = append(s.order, eventID)

	return nil
}
