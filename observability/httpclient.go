package observability

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// InstrumentedTransport wraps base (or http.DefaultTransport when base is nil)
// so every request sent through it opens an OpenTelemetry client span and
// injects the W3C trace context (traceparent/baggage) into the outbound
// headers. The span is a child of whatever span is in the request's context
// (set ctx via http.NewRequestWithContext), or a new root when none is active.
//
// Use it for the BESPOKE http.Clients that do NOT go through ctx.HTTPClient()
// — external APIs (eParaksts/Entrust, the EU LOTL/CELLAR, OCSP responders) and
// service-to-service calls — so those hops show up as client spans and in the
// Tempo service graph. For S2S calls to other azugo services the injected
// trace context lets the callee continue the same trace.
//
// It is safe to apply unconditionally: otelhttp is a no-op when tracing is
// inert (no exporter configured / no active span), so it never changes
// behaviour or adds overhead beyond a cheap wrapper.
func InstrumentedTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return otelhttp.NewTransport(base)
}

// InstrumentHTTPClient sets c.Transport to the instrumented wrapper in place and
// returns c (allocating a client when c is nil). Convenience for the common
// case of instrumenting a service's own http.Client at construction.
func InstrumentHTTPClient(c *http.Client) *http.Client {
	if c == nil {
		c = &http.Client{}
	}
	c.Transport = InstrumentedTransport(c.Transport)
	return c
}
