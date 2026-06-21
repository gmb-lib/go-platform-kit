package broker_test

import (
	"context"
	"encoding/json"
	"testing"

	"azugo.io/azugo"
	"github.com/go-quicktest/qt"
	"github.com/valyala/fasthttp"

	"github.com/gmb-sig/go-platform-kit/broker"
)

func validEnvelope() *broker.Envelope {
	return &broker.Envelope{
		EventType:  "envelope.completed",
		Categories: []broker.Category{broker.CategorySigning},
		Outcome:    broker.OutcomeSuccess,
	}
}

// withCtx runs fn inside a real request handler so it receives a fully
// initialized *azugo.Context (MockContext supplies a nil request and is unusable
// for context operations). fn must only capture into outer variables; assert in
// the test goroutine after withCtx returns.
func withCtx(t *testing.T, fn func(ctx *azugo.Context)) {
	t.Helper()

	app := azugo.NewTestApp()
	app.Get("/t", func(ctx *azugo.Context) {
		fn(ctx)
		ctx.StatusCode(fasthttp.StatusNoContent)
	})
	app.Start(t)

	defer app.Stop()

	resp, err := app.TestClient().Get("/t")
	qt.Assert(t, qt.IsNil(err))
	fasthttp.ReleaseResponse(resp)
}

func TestEnvelope_Validate(t *testing.T) {
	// Unstamped: missing event_id / occurred_at.
	qt.Check(t, qt.IsNotNil(validEnvelope().Validate()))

	missingType := &broker.Envelope{
		EventID:    "e1",
		Categories: []broker.Category{broker.CategorySigning},
		Outcome:    broker.OutcomeSuccess,
	}
	qt.Check(t, qt.IsNotNil(missingType.Validate()))
}

func TestStamp_FillsDefaults(t *testing.T) {
	var ev *broker.Envelope

	withCtx(t, func(ctx *azugo.Context) {
		ev = validEnvelope()
		broker.Stamp(ctx, ev)
	})

	qt.Check(t, qt.Equals(len(ev.EventID), 26)) // ULID
	qt.Check(t, qt.IsFalse(ev.OccurredAt.IsZero()))
	qt.Check(t, qt.IsNil(ev.Validate())) // now valid
}

func TestStamp_StripsTokens(t *testing.T) {
	var ev *broker.Envelope

	withCtx(t, func(ctx *azugo.Context) {
		ev = validEnvelope()
		ev.Attributes = map[string]any{
			"authorization": "Bearer xyz",
			"dpop_proof":    "eyJ...",
			"document_id":   "doc-1",
		}
		broker.Stamp(ctx, ev)
	})

	_, hasAuth := ev.Attributes["authorization"]
	_, hasProof := ev.Attributes["dpop_proof"]
	_, hasDoc := ev.Attributes["document_id"]

	qt.Check(t, qt.IsFalse(hasAuth), qt.Commentf("bearer token must be stripped"))
	qt.Check(t, qt.IsFalse(hasProof), qt.Commentf("dpop proof must be stripped"))
	qt.Check(t, qt.IsTrue(hasDoc), qt.Commentf("document id is safe metadata"))
}

// publishedTransport records the last published message.
type publishedTransport struct {
	topic, key string
	payload    []byte
	err        error
}

func (p *publishedTransport) Publish(_ context.Context, topic, key string, payload []byte) error {
	p.topic, p.key, p.payload = topic, key, payload

	return p.err
}

func TestPublisher_Publish(t *testing.T) {
	tr := &publishedTransport{}
	pub := broker.NewPublisher(tr, "envelope-svc")

	var (
		perr error
		evID string
	)

	withCtx(t, func(ctx *azugo.Context) {
		ev := validEnvelope()
		perr = pub.Publish(ctx, "signing.events", ev)
		evID = ev.EventID
	})

	qt.Assert(t, qt.IsNil(perr))
	qt.Check(t, qt.Equals(tr.topic, "signing.events"))
	qt.Check(t, qt.Equals(tr.key, evID)) // partition key is the event id

	decoded := &broker.Envelope{}
	qt.Assert(t, qt.IsNil(json.Unmarshal(tr.payload, decoded)))
	qt.Check(t, qt.Equals(decoded.EventID, evID))
	qt.Check(t, qt.Equals(decoded.EventType, "envelope.completed"))
}

func TestDispatch_Idempotent(t *testing.T) {
	tr := &publishedTransport{}
	pub := broker.NewPublisher(tr, "envelope-svc")

	withCtx(t, func(ctx *azugo.Context) {
		_ = pub.Publish(ctx, "signing.events", validEnvelope())
	})

	store := broker.NewMemoryIdempotencyStore()

	calls := 0
	handler := func(_ context.Context, _ *broker.Envelope) error {
		calls++

		return nil
	}

	// The same payload delivered twice (at-least-once) is handled exactly once.
	qt.Assert(t, qt.IsNil(broker.Dispatch(context.Background(), tr.payload, store, handler)))
	qt.Assert(t, qt.IsNil(broker.Dispatch(context.Background(), tr.payload, store, handler)))

	qt.Check(t, qt.Equals(calls, 1))
}
