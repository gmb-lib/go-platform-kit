package observability

import (
	"azugo.io/azugo"
	"azugo.io/opentelemetry"
)

// EnableTracing performs the documented azugo.io/opentelemetry wiring once, with
// standard resource attributes. It enables — never re-implements — tracing: it
// calls opentelemetry.Use (which traces router handlers, ctx.HTTPClient, and the
// cache) and registers the returned task with the app.
//
// The service name defaults to the app's AppName when not set via OTEL_SERVICE_NAME.
// If no OTLP endpoint is configured the package returns a no-op task, so the
// service starts cleanly and tracing is simply inert — no code change required.
func EnableTracing(app *azugo.App, cfg *opentelemetry.Configuration) error {
	if cfg == nil {
		cfg = &opentelemetry.Configuration{}
	}

	// Standardize the service name on the application name when the operator has
	// not overridden it via OTEL_SERVICE_NAME.
	if cfg.ServiceName == "" {
		cfg.ServiceName = app.AppName
	}

	t, err := opentelemetry.Use(app, cfg)
	if err != nil {
		return err
	}

	return app.AddTask(t)
}
