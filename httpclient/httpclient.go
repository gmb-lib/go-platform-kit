// Package httpclient adds project conventions over Azugo's ctx.HTTPClient():
// correlation propagation on outbound calls and sane transport defaults.
//
// It owns transport defaults and correlation; go-authbyte owns auth — it
// attaches the DPoP-bound service token + proof. The two compose: build the
// outbound client here, then layer go-authbyte's request options on top.
//
// The W3C `traceparent` header is injected automatically by
// azugo.io/opentelemetry's HTTP-client instrumentation, so this package only
// adds the project correlation_id header.
package httpclient

import (
	"time"

	"azugo.io/azugo"
	"azugo.io/core/http"

	"github.com/gmb-sig/go-platform-kit/correlation"
)

// Default transport conventions. Per-request deadlines still come from the
// inbound request context (ctx.HTTPClient() inherits it); these document the
// fleet defaults a service configures on the Azugo http_client section.
const (
	// DefaultTimeout is the recommended overall timeout for an outbound call.
	DefaultTimeout = 10 * time.Second
	// DefaultMaxRetries is the recommended bound on retries (with backoff).
	DefaultMaxRetries = 2
)

// CorrelationOptions returns the Azugo HTTP request options that propagate the
// request's correlation_id to the upstream service — one option when a
// correlation id is bound, an empty slice otherwise. Spread it into the request
// call (variadic expansion of an empty slice is a no-op), which avoids ever
// passing a typed-nil option into the request pipeline.
func CorrelationOptions(ctx *azugo.Context) []http.RequestOption {
	if cid := correlation.ID(ctx); cid != "" {
		return []http.RequestOption{http.WithHeader(correlation.HeaderCorrelationID, cid)}
	}

	return nil
}

// Outbound returns the context-bound HTTP client targeting baseURL. The client
// is already OpenTelemetry-instrumented (via azugo.io/opentelemetry) and
// inherits the inbound request's deadline and tracing. Spread
// CorrelationOptions(ctx) — and, for authenticated calls, go-authbyte's token
// option — into each request:
//
//	c := httpclient.Outbound(ctx, "https://document-svc")
//	err := c.GetJSON("/v1/documents/"+id, &doc, httpclient.CorrelationOptions(ctx)...)
func Outbound(ctx *azugo.Context, baseURL string) http.Client {
	return ctx.HTTPClient().WithBaseURL(baseURL)
}
