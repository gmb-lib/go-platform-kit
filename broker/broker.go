package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"azugo.io/azugo"
	"github.com/oklog/ulid/v2"

	"github.com/gmb-sig/go-platform-kit/correlation"
	"github.com/gmb-sig/go-platform-kit/observability"
)

// now returns the current time in UTC. occurred_at is a high-precision,
// synced-clock timestamp.
func now() time.Time { return time.Now().UTC() }

// Transport is the minimal broker abstraction go-platform-kit publishes over. A
// concrete implementation (Kafka/NATS/… client) is injected by the service and
// is responsible for the connection, TLS, and per-topic ACLs configured from the
// broker config section. Keeping it abstract keeps go-platform-kit in-process
// glue, not a broker client.
//
// Implementation note: the ctx passed to Publish may be a pooled
// fasthttp/azugo request context. Do NOT derive cancel/timeout contexts from
// it directly (context.WithTimeout(ctx, …)) — the stdlib spawns a watcher
// goroutine on the parent that can outlive the request and race with the
// context being reset for reuse. Detach first:
// context.WithTimeout(context.WithoutCancel(ctx), …).
type Transport interface {
	// Publish sends payload to topic. key is the partition/ordering key (the
	// event id), letting the transport preserve per-key ordering.
	Publish(ctx context.Context, topic, key string, payload []byte) error
}

// Publisher serializes and publishes events as the event envelope, stamping
// correlation/trace ids from the request.
type Publisher struct {
	transport Transport
	// service is the logical service id, used as a metric label.
	service string
}

// NewPublisher returns a Publisher over the given transport. service is the
// logical service id (config.BaseConfiguration.ServiceName).
func NewPublisher(transport Transport, service string) *Publisher {
	return &Publisher{transport: transport, service: service}
}

// Publish stamps, validates, and publishes an event to topic. It mutates ev to
// fill in the event id, occurrence time, and correlation/trace ids from the
// request when they are not already set, so callers only supply the event's
// own content.
func (p *Publisher) Publish(ctx *azugo.Context, topic string, ev *Envelope) error {
	if p == nil || p.transport == nil {
		return errors.New("broker: publisher has no transport")
	}

	if ev == nil {
		return errors.New("broker: nil envelope")
	}

	Stamp(ctx, ev)

	if err := ev.Validate(); err != nil {
		return err
	}

	payload, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("broker: marshal envelope: %w", err)
	}

	err = p.transport.Publish(ctx, topic, ev.EventID, payload)

	observability.IncCounter(observability.MetricBrokerPublishTotal, map[string]string{
		observability.LabelService: p.service,
		observability.LabelTopic:   topic,
		observability.LabelOutcome: outcome(err),
	})

	if err != nil {
		return fmt.Errorf("broker: publish to %q: %w", topic, err)
	}

	return nil
}

// Stamp fills in the envelope's identity and correlation fields from the request
// where they are not already set: a fresh ULID event id, the current time, and
// the correlation_id/trace_id bound to the context. It also strips
// bearer-token-shaped attribute keys — events carry correlation, never tokens.
//
// Mutation contract: Stamp mutates ev (that is its job), but it never mutates
// the map the caller passed as Attributes — when stripping is needed, ev's
// Attributes is replaced with a filtered copy, so a caller-owned map is safe to
// reuse.
func Stamp(ctx *azugo.Context, ev *Envelope) {
	if ev.EventID == "" {
		ev.EventID = ulid.Make().String()
	}

	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = now()
	}

	ids := correlation.FromContext(ctx)
	if ev.CorrelationID == "" {
		ev.CorrelationID = ids.CorrelationID
	}

	if ev.TraceID == "" {
		ev.TraceID = ids.TraceID
	}

	ev.Attributes = stripTokens(ev.Attributes)
}

// Validate checks the envelope carries the minimum a sink needs: an event
// type, at least one category, and an outcome. Identity fields are filled by
// Stamp before validation.
func (ev *Envelope) Validate() error {
	if ev.EventID == "" {
		return errors.New("broker: envelope missing event_id")
	}

	if ev.OccurredAt.IsZero() {
		return errors.New("broker: envelope missing occurred_at")
	}

	if ev.EventType == "" {
		return errors.New("broker: envelope missing event_type")
	}

	if len(ev.Categories) == 0 {
		return errors.New("broker: envelope missing category")
	}

	if ev.Outcome == "" {
		return errors.New("broker: envelope missing outcome")
	}

	return nil
}

// tokenKeySubstrings are attribute-key fragments that signal a bearer token or
// credential and must never ride a broker event.
var tokenKeySubstrings = []string{
	"authorization", "bearer", "token", "dpop", "proof", "secret",
	"password", "credential", "cookie", "api_key", "apikey", "private_key",
}

// stripTokens removes any attribute whose key looks like a credential. It is a
// defensive backstop; emitters should not place tokens in attributes at all.
// It never mutates the input map: when something must be stripped it returns a
// filtered copy (copy-on-write), so caller-owned maps stay intact.
func stripTokens(attrs map[string]any) map[string]any {
	if len(attrs) == 0 {
		return attrs
	}

	var out map[string]any // allocated only if something is stripped

	for k := range attrs {
		lk := strings.ToLower(k)
		for _, s := range tokenKeySubstrings {
			if strings.Contains(lk, s) {
				if out == nil {
					out = make(map[string]any, len(attrs))
					for ck, cv := range attrs {
						out[ck] = cv
					}
				}

				delete(out, k)

				break
			}
		}
	}

	if out == nil {
		return attrs
	}

	return out
}

func outcome(err error) string {
	if err != nil {
		return observability.OutcomeError
	}

	return observability.OutcomeSuccess
}
