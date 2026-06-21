// Package natsbroker is the NATS JetStream implementation of the go-platform-kit
// broker abstraction. It provides a publish broker.Transport and a durable
// pull Consumer that drives broker.Dispatch, so services publish and consume the
// frozen event envelope over JetStream without re-implementing broker plumbing.
//
// This is the one package in go-platform-kit that imports a concrete broker
// client (nats.go), keeping the core broker package transport-agnostic
// in-process glue. Import natsbroker ONLY in services that actually talk to
// NATS (producers via Transport, sinks via Consumer); services that don't never
// pull the nats.go dependency into their binary.
//
// Connection material comes from the platform config Broker section
// (BROKER_URL / BROKER_TLS_*); callers map those fields into Config.
package natsbroker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/gmb-sig/go-platform-kit/broker"
)

// publishTimeout bounds a single JetStream publish. Detached from the (possibly
// pooled) request context before use — see Transport.Publish.
const publishTimeout = 5 * time.Second

// Config carries the NATS connection details, sourced from the platform config
// Broker section. URL is non-secret; the TLS material is the secret PEM resolved
// via the Vault-agent BROKER_TLS_*_FILE convention.
type Config struct {
	// URL is the NATS endpoint, e.g. "nats://broker:4222" or "tls://broker:4222".
	URL string
	// TLSCert / TLSKey / TLSCA are client TLS material (PEM). When TLSCert+TLSKey
	// are set, mutual TLS is used; TLSCA pins the server roots.
	TLSCert string
	TLSKey  string
	TLSCA   string
	// Name is the connection name (the logical service id) for broker-side
	// monitoring; use config.BaseConfiguration.ServiceName.
	Name string
}

// Conn wraps a NATS connection and its JetStream context.
type Conn struct {
	nc *nats.Conn
	js jetstream.JetStream
}

// Connect opens a NATS connection and JetStream context per cfg. The connection
// reconnects indefinitely so a transient broker outage degrades gracefully.
func Connect(cfg Config) (*Conn, error) {
	if cfg.URL == "" {
		return nil, errors.New("natsbroker: empty broker url")
	}

	opts := []nats.Option{
		nats.Name(cfg.Name),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * time.Second),
	}

	if cfg.TLSCert != "" || cfg.TLSCA != "" {
		tlsCfg, err := tlsConfig(cfg)
		if err != nil {
			return nil, err
		}

		opts = append(opts, nats.Secure(tlsCfg))
	}

	nc, err := nats.Connect(cfg.URL, opts...)
	if err != nil {
		return nil, fmt.Errorf("natsbroker: connect %q: %w", cfg.URL, err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()

		return nil, fmt.Errorf("natsbroker: jetstream: %w", err)
	}

	return &Conn{nc: nc, js: js}, nil
}

// tlsConfig builds a *tls.Config from the PEM material in cfg.
func tlsConfig(cfg Config) (*tls.Config, error) {
	t := &tls.Config{MinVersion: tls.VersionTLS12}

	if cfg.TLSCert != "" && cfg.TLSKey != "" {
		cert, err := tls.X509KeyPair([]byte(cfg.TLSCert), []byte(cfg.TLSKey))
		if err != nil {
			return nil, fmt.Errorf("natsbroker: client cert: %w", err)
		}

		t.Certificates = []tls.Certificate{cert}
	}

	if cfg.TLSCA != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(cfg.TLSCA)) {
			return nil, errors.New("natsbroker: invalid TLS CA PEM")
		}

		t.RootCAs = pool
	}

	return t, nil
}

// JetStream exposes the underlying JetStream context for stream/consumer admin.
func (c *Conn) JetStream() jetstream.JetStream { return c.js }

// Ping reports broker reachability for readiness probes.
func (c *Conn) Ping() error {
	if c == nil || c.nc == nil {
		return errors.New("natsbroker: nil connection")
	}

	if !c.nc.IsConnected() {
		return errors.New("natsbroker: not connected")
	}

	return nil
}

// Close drains in-flight messages and closes the connection.
func (c *Conn) Close() {
	if c != nil && c.nc != nil {
		_ = c.nc.Drain()
	}
}

// StreamConfig describes the JetStream stream to ensure exists. For the audit
// trail use a long/unlimited MaxAge (integrity, not space, is the priority) and a
// Duplicates window at least as long as the redelivery window, so the server
// deduplicates by Msg-Id (the event id) as a backstop beneath the sink's own
// event_id idempotency.
type StreamConfig struct {
	Name       string        // e.g. "AUDIT"
	Subjects   []string      // e.g. []string{"audit.>"}
	MaxAge     time.Duration // 0 = unlimited
	Duplicates time.Duration // server-side dedup window (e.g. 2m)
}

// EnsureStream idempotently creates or updates a file-backed stream. Typically
// called once at startup by the sink that owns the subject space.
func (c *Conn) EnsureStream(ctx context.Context, sc StreamConfig) error {
	if c == nil || c.js == nil {
		return errors.New("natsbroker: nil connection")
	}

	cfg := jetstream.StreamConfig{
		Name:       sc.Name,
		Subjects:   sc.Subjects,
		Storage:    jetstream.FileStorage,
		Retention:  jetstream.LimitsPolicy,
		MaxAge:     sc.MaxAge,
		Duplicates: sc.Duplicates,
	}

	if _, err := c.js.CreateOrUpdateStream(ctx, cfg); err != nil {
		return fmt.Errorf("natsbroker: ensure stream %q: %w", sc.Name, err)
	}

	return nil
}

// Transport is the NATS JetStream implementation of broker.Transport. Publish
// sets the JetStream Msg-Id to the key (the event id) so the server deduplicates
// redeliveries within the stream's Duplicates window.
type Transport struct {
	js jetstream.JetStream
}

// Transport satisfies broker.Transport.
var _ broker.Transport = (*Transport)(nil)

// NewTransport returns a publish Transport bound to the connection's JetStream.
func NewTransport(c *Conn) *Transport { return &Transport{js: c.js} }

// Publish sends payload to topic with key as the JetStream Msg-Id.
func (t *Transport) Publish(ctx context.Context, topic, key string, payload []byte) error {
	if t == nil || t.js == nil {
		return errors.New("natsbroker: transport has no jetstream")
	}

	// The broker.Transport contract warns that ctx may be a pooled azugo request
	// context; detach before adding a timeout so the stdlib watcher goroutine
	// cannot outlive the request.
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), publishTimeout)
	defer cancel()

	var opts []jetstream.PublishOpt
	if key != "" {
		opts = append(opts, jetstream.WithMsgID(key))
	}

	if _, err := t.js.Publish(pctx, topic, payload, opts...); err != nil {
		return fmt.Errorf("natsbroker: publish %q: %w", topic, err)
	}

	return nil
}
