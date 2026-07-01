// Package platform is the single bootstrap entrypoint. One call to Setup wires
// Azugo's telemetry + the project glue identically across every service:
// logging with PII/secret redaction, metric conventions, OpenTelemetry tracing,
// the correlation middleware, and the error taxonomy.
//
// A service calls it from its App.init(), right after server.New(...):
//
//	func (a *App) init() error {
//	 if err := platform.Setup(a.App, platform.Options{
//	 Config: a.config.BaseConfiguration,
//	 }); err != nil {
//	 return err
//	 }
//	 // …service-specific wiring (stores, routes, go-authbyte, audit emitters)…
//	 return nil
//	}
package platform

import (
	"errors"

	"azugo.io/azugo"

	"github.com/gmb-lib/go-platform-kit/config"
	"github.com/gmb-lib/go-platform-kit/correlation"
	pkerrors "github.com/gmb-lib/go-platform-kit/errors"
	"github.com/gmb-lib/go-platform-kit/observability"
)

// Options configures Setup.
type Options struct {
	// Config is the service's embedded go-platform-kit base configuration. It
	// must be loaded (the service's server.New / config Load has run) before
	// Setup is called. Required.
	Config *config.BaseConfiguration

	// Redaction overrides the log redaction policy. When nil, the fleet-standard
	// observability.DefaultRedactionPolicy is used. Redaction is always enabled.
	Redaction *observability.RedactionPolicy

	// PublicErrors installs the error renderer in public-boundary mode: every
	// error response is projected to the public envelope — the internal source
	// and hop chain are stripped and the detail is withheld unless marked
	// public-safe. Set it only on the single public-facing service (the BFF);
	// internal services leave it false so service-to-service errors carry the
	// full envelope for relay and logging.
	PublicErrors bool
}

// Setup wires the cross-cutting concerns onto app, in the order they must run:
//
// 1. Tracing — enables azugo.io/opentelemetry (registers the trace middleware
// and instrumentation). Done first so the correlation middleware can read
// the active trace_id. Inert when no OTLP endpoint is configured.
// 2. Redaction — wraps the application logger so no log line can leak a secret
// or PII. Must be installed before any request is served.
// 3. Correlation — installs the middleware that binds correlation_id and the
// trace ids to each request and to every log line.
//
// After Setup the service has standardized logging+redaction, metrics, tracing,
// and correlation installed — without copy-paste. The error taxonomy
// (package errors) is then available for handlers to map domain/data errors to
// consistent HTTP responses.
func Setup(app *azugo.App, opts Options) error {
	if app == nil {
		return errors.New("platform: nil app")
	}

	if opts.Config == nil {
		return errors.New("platform: Options.Config is required")
	}

	// 1. OpenTelemetry tracing (enable, never re-implement).
	if err := observability.EnableTracing(app, opts.Config.Telemetry); err != nil {
		return err
	}

	// 2. Log redaction — compliance guardrail.
	observability.EnableRedaction(app, opts.Redaction)

	// 3. Correlation middleware — the project-only piece. Runs
	// after the tracing middleware (registered in step 1) so trace_id/span_id
	// are available to bind alongside correlation_id.
	app.Use(correlation.Middleware())

	// 4. Uniform error envelope (RFC 9457). One global handler renders every
	// error — produced here or relayed from downstream — as
	// application/problem+json with a stable code, this service as the source,
	// and the request trace id, replacing both the hand-rolled error bodies and
	// the framework's default error shape. app.AppName is the same service id
	// already stamped on every log line, so the source has a single origin.
	app.RouterOptions().ErrorHandler = pkerrors.Handler(app.AppName, opts.PublicErrors)

	return nil
}
