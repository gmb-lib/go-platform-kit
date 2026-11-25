package observability

import (
	"azugo.io/azugo"
	"azugo.io/opentelemetry"
	"go.opentelemetry.io/otel"
)

// EnableTracing performs the documented azugo.io/opentelemetry wiring once, with
// standard resource attributes. It enables — never re-implements — tracing: it
// calls opentelemetry.Use (which traces router handlers, ctx.HTTPClient, and the
// cache) and registers the returned task with the app.
//
// The service name defaults to the app's AppName when not set via OTEL_SERVICE_NAME.
// If no OTLP endpoint is configured the package returns a no-op task, so the
// service starts cleanly and tracing is simply inert — no code change required.
//
// opts is forwarded to opentelemetry.Use as-is, so a service with instrumentation
// needs beyond the framework's built-in router/HTTP-client/cache tracing (e.g. a
// custom InstrumentationRecorder for DB query tracing) can supply it without
// losing platform.Setup — most callers pass none.
//
// The cache is traced by this package's own recorder, placed before opts, in the
// framework's stead: the same spans and counts, never the key, which is often a
// credential (a session id, a sign-in code). A recorder in opts for the cache's
// operations therefore never runs for them.
func EnableTracing(app *azugo.App, cfg *opentelemetry.Configuration, opts ...opentelemetry.Option) error {
	if cfg == nil {
		cfg = &opentelemetry.Configuration{}
	}

	// Standardize the service name on the application name when the operator has
	// not overridden it via OTEL_SERVICE_NAME.
	if cfg.ServiceName == "" {
		cfg.ServiceName = app.AppName
	}

	t, err := opentelemetry.Use(app, cfg, append([]opentelemetry.Option{cacheTracing(otel.GetMeterProvider)}, opts...)...)
	if err != nil {
		return err
	}

	return app.AddTask(t)
}
