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
// The cache and the rate limiter are traced by this package's own recorders,
// placed before opts, in the framework's stead: the same spans and counts,
// never the key, which for a cache is often a credential (a session id, a
// sign-in code) and for a rate limiter personal data (a client's address, a
// person's subject). A recorder in opts for those operations, or a catch-all
// one, therefore never runs for them. A span name formatter in opts is still
// asked first and is handed the key among its arguments: it must name a span by
// the operation and the instance, never by the key.
func EnableTracing(app *azugo.App, cfg *opentelemetry.Configuration, opts ...opentelemetry.Option) error {
	if cfg == nil {
		cfg = &opentelemetry.Configuration{}
	}

	// Standardize the service name on the application name when the operator has
	// not overridden it via OTEL_SERVICE_NAME.
	if cfg.ServiceName == "" {
		cfg.ServiceName = app.AppName
	}

	own := []opentelemetry.Option{cacheTracing(otel.GetMeterProvider), rateLimitTracing()}
	t, err := opentelemetry.Use(app, cfg, append(own, opts...)...)
	if err != nil {
		return err
	}

	return app.AddTask(t)
}
