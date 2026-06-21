package natsbroker

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/go-quicktest/qt"

	"github.com/gmb-sig/go-platform-kit/broker"
)

func TestConnect_EmptyURL(t *testing.T) {
	_, err := Connect(Config{})
	qt.Check(t, qt.IsNotNil(err))
}

func TestTLSConfig_BadCA(t *testing.T) {
	_, err := tlsConfig(Config{TLSCA: "not a pem block"})
	qt.Check(t, qt.IsNotNil(err))
}

func TestTLSConfig_Empty(t *testing.T) {
	c, err := tlsConfig(Config{})
	qt.Assert(t, qt.IsNil(err))
	qt.Check(t, qt.IsTrue(c.MinVersion == 0x0303)) // TLS 1.2
}

func TestTransport_NilJetStream(t *testing.T) {
	tr := &Transport{}
	err := tr.Publish(context.Background(), "audit.signing", "k", []byte("{}"))
	qt.Check(t, qt.IsNotNil(err))
}

func TestNewConsumer_NilHandler(t *testing.T) {
	_, err := NewConsumer(context.Background(), nil, ConsumerConfig{}, nil, nil, nil)
	qt.Check(t, qt.IsNotNil(err))
}

func TestNewConsumer_NilConn(t *testing.T) {
	handler := func(context.Context, *broker.Envelope) error { return nil }
	_, err := NewConsumer(context.Background(), nil, ConsumerConfig{}, nil, handler, nil)
	qt.Check(t, qt.IsNotNil(err))
}

// TestIntegration_PublishConsume exercises the full Connect → EnsureStream →
// publish → durable consume round trip against a real NATS JetStream server. It
// is skipped unless NATS_TEST_URL is set (e.g. "nats://127.0.0.1:4222" from a
// `nats -js` container), so it never fails in a serverless CI run.
func TestIntegration_PublishConsume(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to run the JetStream integration test")
	}

	conn, err := Connect(Config{URL: url, Name: "natsbroker-test"})
	qt.Assert(t, qt.IsNil(err))

	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	qt.Assert(t, qt.IsNil(conn.EnsureStream(ctx, StreamConfig{
		Name:       "AUDIT_TEST",
		Subjects:   []string{"audit.test.>"},
		Duplicates: 2 * time.Minute,
	})))

	got := make(chan string, 1)
	handler := func(_ context.Context, ev *broker.Envelope) error {
		got <- ev.EventID

		return nil
	}

	cons, err := NewConsumer(ctx, conn, ConsumerConfig{
		Stream:        "AUDIT_TEST",
		Durable:       "natsbroker-test",
		FilterSubject: "audit.test.signing",
	}, broker.NewMemoryIdempotencyStore(), handler, nil)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsNil(cons.Start(ctx)))

	defer cons.Stop()

	ev := &broker.Envelope{
		EventID:    "01TESTEVENTID00000000000000",
		OccurredAt: time.Now().UTC(),
		EventType:  "signing.applied",
		Categories: []broker.Category{broker.CategorySigning},
		Outcome:    broker.OutcomeSuccess,
	}
	payload, err := json.Marshal(ev)
	qt.Assert(t, qt.IsNil(err))

	tr := NewTransport(conn)
	qt.Assert(t, qt.IsNil(tr.Publish(ctx, "audit.test.signing", ev.EventID, payload)))

	select {
	case id := <-got:
		qt.Check(t, qt.Equals(id, ev.EventID))
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the consumed event")
	}
}
