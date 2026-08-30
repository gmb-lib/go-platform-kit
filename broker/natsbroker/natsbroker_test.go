package natsbroker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/go-quicktest/qt"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/gmb-lib/go-platform-kit/broker"
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

	// Start from nothing. The published event id is fixed, so a stream left over
	// from an earlier run inside its own Duplicates window deduplicates the
	// publish away and the durable consumer, whose cursor is already past it,
	// waits forever — the test then reports a broken consumer that is fine.
	_ = conn.JetStream().DeleteStream(ctx, "AUDIT_TEST")

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

func TestMaxBytes_UnlimitedIsExplicit(t *testing.T) {
	// Our convention is "0 = unlimited"; JetStream spells unlimited -1. Anything
	// positive passes through untouched.
	qt.Check(t, qt.Equals(maxBytes(0), int64(-1)))
	qt.Check(t, qt.Equals(maxBytes(-1), int64(-1)))
	qt.Check(t, qt.Equals(maxBytes(134217728), int64(134217728)))
}

// TestIntegration_StreamMaxBytes proves the three things a size cap is bought
// for: that it is applied, that adding one to a stream which already exists
// takes effect (the sink calls EnsureStream at every start), and that reaching
// it drops the OLDEST messages while publishing keeps succeeding — the opposite
// policy would start refusing the newest events, which is the failure a bounded
// audit copy must not have. Skipped unless NATS_TEST_URL is set.
func TestIntegration_StreamMaxBytes(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to run the JetStream integration test")
	}

	conn, err := Connect(Config{URL: url, Name: "natsbroker-cap-test"})
	qt.Assert(t, qt.IsNil(err))

	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const (
		stream   = "AUDIT_CAP_TEST"
		subject  = "audit.cap.signing"
		capBytes = 4096
		sent     = 40
	)

	// Start from nothing, so the first EnsureStream below is a create.
	_ = conn.JetStream().DeleteStream(ctx, stream)

	base := StreamConfig{Name: stream, Subjects: []string{"audit.cap.>"}}

	qt.Assert(t, qt.IsNil(conn.EnsureStream(ctx, base)))
	qt.Assert(t, qt.Equals(streamInfo(ctx, t, conn, stream).Config.MaxBytes, int64(-1)))

	// The same call with a cap must UPDATE the stream that already exists.
	capped := base
	capped.MaxBytes = capBytes
	qt.Assert(t, qt.IsNil(conn.EnsureStream(ctx, capped)))

	info := streamInfo(ctx, t, conn, stream)
	qt.Assert(t, qt.Equals(info.Config.MaxBytes, int64(capBytes)))
	qt.Check(t, qt.Equals(info.Config.Discard, jetstream.DiscardOld))

	// Publish well past the cap; every publish must still succeed.
	tr := NewTransport(conn)
	payload := bytes.Repeat([]byte("x"), 512)

	for i := range sent {
		qt.Assert(t, qt.IsNil(tr.Publish(ctx, subject, fmt.Sprintf("cap-%02d", i), payload)))
	}

	info = streamInfo(ctx, t, conn, stream)
	qt.Check(t, qt.IsTrue(info.State.Bytes <= capBytes))     // the cap holds
	qt.Check(t, qt.IsTrue(info.State.Msgs < sent))           // something was discarded
	qt.Check(t, qt.Equals(info.State.LastSeq, uint64(sent))) // the newest survived
	qt.Check(t, qt.IsTrue(info.State.FirstSeq > 1))          // the oldest are the ones gone

	qt.Assert(t, qt.IsNil(conn.JetStream().DeleteStream(ctx, stream)))
}

// streamInfo reads a stream's live configuration and state.
func streamInfo(ctx context.Context, t *testing.T, c *Conn, name string) *jetstream.StreamInfo {
	t.Helper()

	s, err := c.JetStream().Stream(ctx, name)
	qt.Assert(t, qt.IsNil(err))

	info, err := s.Info(ctx)
	qt.Assert(t, qt.IsNil(err))

	return info
}
