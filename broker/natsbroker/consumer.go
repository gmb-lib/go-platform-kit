package natsbroker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"

	"github.com/gmb-sig/go-platform-kit/broker"
)

// defaultAckWait is how long JetStream waits for an ack before redelivering.
const defaultAckWait = 30 * time.Second

// ConsumerConfig configures a durable pull consumer bound to a stream subject.
type ConsumerConfig struct {
	// Stream is the JetStream stream name to bind (e.g. "AUDIT").
	Stream string
	// Durable is the durable consumer name; it makes the cursor survive
	// restarts, so a redeploy resumes without loss or replay-from-zero.
	Durable string
	// FilterSubject narrows the stream to one subject (e.g. "audit.signing").
	FilterSubject string
	// AckWait is the redelivery timeout (defaultAckWait when zero).
	AckWait time.Duration
	// MaxDeliver caps redelivery attempts (0 = unlimited). A poison message that
	// can never be processed should be capped + dead-lettered by the caller.
	MaxDeliver int
}

// Consumer drives broker.Dispatch over a durable JetStream consumer. It is
// framework-agnostic: a service wraps Start/Stop in its own task runner (e.g. an
// azugo core.Tasker) so the same code runs standalone or bundled inside another
// service. Handlers must be idempotent — delivery is at-least-once.
type Consumer struct {
	cons    jetstream.Consumer
	store   broker.IdempotencyStore
	handler broker.Handler
	log     *zap.Logger
	cc      jetstream.ConsumeContext
}

// NewConsumer creates or updates the durable consumer and returns a Consumer.
// store may be nil (the sink's own event_id uniqueness is the durable dedup
// backstop); handler is required.
func NewConsumer(
	ctx context.Context,
	c *Conn,
	cc ConsumerConfig,
	store broker.IdempotencyStore,
	handler broker.Handler,
	log *zap.Logger,
) (*Consumer, error) {
	if handler == nil {
		return nil, errors.New("natsbroker: nil handler")
	}

	if c == nil || c.js == nil {
		return nil, errors.New("natsbroker: nil connection")
	}

	if log == nil {
		log = zap.NewNop()
	}

	ackWait := cc.AckWait
	if ackWait <= 0 {
		ackWait = defaultAckWait
	}

	cons, err := c.js.CreateOrUpdateConsumer(ctx, cc.Stream, jetstream.ConsumerConfig{
		Durable:       cc.Durable,
		FilterSubject: cc.FilterSubject,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       ackWait,
		MaxDeliver:    cc.MaxDeliver,
	})
	if err != nil {
		return nil, fmt.Errorf("natsbroker: ensure consumer %q on %q: %w", cc.Durable, cc.Stream, err)
	}

	return &Consumer{cons: cons, store: store, handler: handler, log: log}, nil
}

// Start begins consuming in the background. Each message is handed to
// broker.Dispatch (decode + dedupe + handler): success acks; any error naks so
// JetStream redelivers (an audit event is never silently dropped). Call Stop to
// halt. ctx scopes the dispatched work (it flows into the handler / DB calls).
func (c *Consumer) Start(ctx context.Context) error {
	if c == nil || c.cons == nil {
		return errors.New("natsbroker: nil consumer")
	}

	cc, err := c.cons.Consume(func(msg jetstream.Msg) {
		if derr := broker.Dispatch(ctx, msg.Data(), c.store, c.handler); derr != nil {
			c.log.Warn("broker dispatch failed; nak for redelivery",
				zap.String("subject", msg.Subject()),
				zap.Error(derr),
			)

			_ = msg.Nak()

			return
		}

		if ackErr := msg.Ack(); ackErr != nil {
			// The event was processed; a failed ack only causes a redelivery,
			// which the idempotent handler absorbs.
			c.log.Warn("broker ack failed; event will be redelivered",
				zap.String("subject", msg.Subject()),
				zap.Error(ackErr),
			)
		}
	})
	if err != nil {
		return fmt.Errorf("natsbroker: consume: %w", err)
	}

	c.cc = cc

	return nil
}

// Stop halts consumption. Safe to call when not started.
func (c *Consumer) Stop() {
	if c != nil && c.cc != nil {
		c.cc.Stop()
	}
}
