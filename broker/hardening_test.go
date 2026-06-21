package broker_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"azugo.io/azugo"
	"github.com/go-quicktest/qt"

	"github.com/gmb-sig/go-platform-kit/broker"
)

// payloadFor marshals a minimal valid envelope with the given event id.
func payloadFor(t *testing.T, eventID string) []byte {
	t.Helper()

	ev := &broker.Envelope{
		EventID:    eventID,
		OccurredAt: time.Now().UTC(),
		EventType:  "envelope.completed",
		Categories: []broker.Category{broker.CategorySigning},
		Outcome:    broker.OutcomeSuccess,
	}

	payload, err := json.Marshal(ev)
	qt.Assert(t, qt.IsNil(err))

	return payload
}

func TestDispatch_HandlerFailureIsRetriable(t *testing.T) {
	store := broker.NewMemoryIdempotencyStore()
	payload := payloadFor(t, "ev-retry-1")

	calls := 0
	failing := func(_ context.Context, _ *broker.Envelope) error {
		calls++

		return errors.New("sink unavailable")
	}

	// First delivery fails — the event must NOT be marked processed.
	qt.Assert(t, qt.IsNotNil(broker.Dispatch(context.Background(), payload, store, failing)))

	// Redelivery (after the transient failure) must invoke the handler again —
	// the event was never successfully processed, so it cannot be a duplicate.
	ok := func(_ context.Context, _ *broker.Envelope) error {
		calls++

		return nil
	}
	qt.Assert(t, qt.IsNil(broker.Dispatch(context.Background(), payload, store, ok)))

	// And only now is it deduplicated.
	qt.Assert(t, qt.IsNil(broker.Dispatch(context.Background(), payload, store, ok)))

	qt.Check(t, qt.Equals(calls, 2), qt.Commentf("failed attempt + successful retry; duplicate skipped"))
}

// failingMarkStore wraps the memory store and fails MarkProcessed once.
type failingMarkStore struct {
	*broker.MemoryIdempotencyStore

	failures int
}

func (s *failingMarkStore) MarkProcessed(ctx context.Context, eventID string) error {
	if s.failures > 0 {
		s.failures--

		return errors.New("store unavailable")
	}

	return s.MemoryIdempotencyStore.MarkProcessed(ctx, eventID)
}

func TestDispatch_MarkFailureSurfacesError(t *testing.T) {
	store := &failingMarkStore{MemoryIdempotencyStore: broker.NewMemoryIdempotencyStore(), failures: 1}
	payload := payloadFor(t, "ev-mark-1")

	calls := 0
	handler := func(_ context.Context, _ *broker.Envelope) error {
		calls++

		return nil
	}

	// Handler succeeds but the mark fails → Dispatch must surface the error so
	// the message is redelivered (handlers are idempotent by contract).
	qt.Assert(t, qt.IsNotNil(broker.Dispatch(context.Background(), payload, store, handler)))

	// Redelivery: handled again, marked this time, then deduplicated.
	qt.Assert(t, qt.IsNil(broker.Dispatch(context.Background(), payload, store, handler)))
	qt.Assert(t, qt.IsNil(broker.Dispatch(context.Background(), payload, store, handler)))

	qt.Check(t, qt.Equals(calls, 2))
}

func TestMemoryIdempotencyStore_EvictsFIFO(t *testing.T) {
	store := broker.NewMemoryIdempotencyStoreWithCapacity(2)
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		qt.Assert(t, qt.IsNil(store.MarkProcessed(ctx, fmt.Sprintf("ev-%d", i))))
	}

	seen1, _ := store.Seen(ctx, "ev-1")
	seen2, _ := store.Seen(ctx, "ev-2")
	seen3, _ := store.Seen(ctx, "ev-3")

	qt.Check(t, qt.IsFalse(seen1), qt.Commentf("oldest id evicted at capacity"))
	qt.Check(t, qt.IsTrue(seen2))
	qt.Check(t, qt.IsTrue(seen3))
}

func TestStamp_DoesNotMutateCallerAttributes(t *testing.T) {
	caller := map[string]any{
		"authorization": "Bearer xyz", // will be stripped from the envelope…
		"document_id":   "doc-1",
	}

	var ev *broker.Envelope

	withCtx(t, func(ctx *azugo.Context) {
		ev = validEnvelope()
		ev.Attributes = caller
		broker.Stamp(ctx, ev)
	})

	// …but the caller-owned map must stay intact (copy-on-write).
	qt.Check(t, qt.Equals(len(caller), 2), qt.Commentf("caller map must not be mutated"))

	_, stripped := ev.Attributes["authorization"]
	qt.Check(t, qt.IsFalse(stripped))
}
