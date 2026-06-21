package observability

import (
	"testing"

	"azugo.io/azugo"
	"azugo.io/opentelemetry"
	"github.com/go-quicktest/qt"
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
