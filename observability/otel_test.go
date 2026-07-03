package observability

import (
	"context"
	"testing"

	"azugo.io/azugo"
	"azugo.io/opentelemetry"
	"github.com/go-quicktest/qt"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func TestEnableTracing_InertWithoutEndpoint(t *testing.T) {
	app := azugo.New()
	app.AppName = "document-svc"

	// No OTLP endpoint configured: tracing is disabled, the call succeeds, and
	// the service can start cleanly (tracing inert with no endpoint).
	qt.Assert(t, qt.IsNil(EnableTracing(app, nil)))
}

func TestEnableTracing_Disabled(t *testing.T) {
	app := azugo.New()
	app.AppName = "document-svc"

	qt.Assert(t, qt.IsNil(EnableTracing(app, &opentelemetry.Configuration{Disabled: true})))
}

func TestEnableTracing_DefaultsServiceName(t *testing.T) {
	app := azugo.New()
	app.AppName = "document-svc"

	cfg := &opentelemetry.Configuration{}
	qt.Assert(t, qt.IsNil(EnableTracing(app, cfg)))
	// Service name is standardized on the app name when not set via env.
	qt.Check(t, qt.Equals(cfg.ServiceName, "document-svc"))
}

// noopRecorder is a minimal InstrumentationRecorderFunc stand-in for a
// service's custom instrumentation (e.g. DB query tracing) — this test only
// asserts the option reaches opentelemetry.Use without error, not its runtime
// behavior, which belongs to azugo.io/opentelemetry's own test suite.
func noopRecorder(_ context.Context, _ trace.Tracer, _ propagation.TextMapPropagator, _ opentelemetry.InstrumentationSpanNameFormatter, _ string, _ ...any) (func(err error), bool) {
	return nil, false
}

func TestEnableTracing_ForwardsOptions(t *testing.T) {
	app := azugo.New()
	app.AppName = "document-svc"

	// A service with a custom instrumentation recorder (e.g. jsondb DB query
	// tracing) must be able to supply it through EnableTracing/platform.Setup
	// without losing platform.Setup — this is the option this test exercises.
	opt := opentelemetry.InstrumentationRecorder("db", noopRecorder)
	qt.Assert(t, qt.IsNil(EnableTracing(app, &opentelemetry.Configuration{}, opt)))
}
