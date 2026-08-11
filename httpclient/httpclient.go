// Package httpclient adds project conventions over Azugo's ctx.HTTPClient():
// correlation propagation on outbound calls and sane transport defaults.
//
// It owns transport defaults and correlation; go-authbyte owns auth — it
// attaches the DPoP-bound service token + proof. The two compose: build the
// outbound client here, then layer go-authbyte's request options on top.
//
// The W3C `traceparent` header is injected automatically by
// azugo.io/opentelemetry's HTTP-client instrumentation, so this package only
// adds the project correlation_id and app-instance-id headers.
package httpclient

import (
	"time"

	"azugo.io/azugo"
	"azugo.io/core/http"
	"github.com/valyala/fasthttp"

	"github.com/gmb-lib/go-platform-kit/correlation"
	"github.com/gmb-lib/go-platform-kit/propagation"
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
// request's correlation_id - and, when present on the inbound request, the
// calling app instance id - to the upstream service. Spread it into the
// request call (variadic expansion of an empty slice is a no-op), which
// avoids ever passing a typed-nil option into the request pipeline.
//
// The app instance id is forwarded unchanged (never minted here) so that a
// failure audited at whichever internal service actually originates it (see
// errors.FailureHook) still has it, even when the end-user's request reached
// that service only via one or more internal hops.
func CorrelationOptions(ctx *azugo.Context) []http.RequestOption {
	opts := make([]http.RequestOption, 0, 2)

	if cid := correlation.ID(ctx); cid != "" {
		opts = append(opts, http.WithHeader(correlation.HeaderCorrelationID, cid))
	}

	if iid := correlation.AppInstanceID(ctx); iid != "" {
		opts = append(opts, http.WithHeader(propagation.HeaderAppInstanceID, iid))
	}

	if len(opts) == 0 {
		return nil
	}

	return opts
}

// SetCorrelationHeaders sets the correlation_id and (when present) app
// instance id headers directly on a raw fasthttp request header.
//
// Use this instead of CorrelationOptions when a client builds its request via
// ctx.HTTPClient().NewRequest() rather than the option-based Client.Do*
// helpers (e.g. to control body/content-type/auth headers by hand) — both
// paths must forward the same two headers, so this is the one place that
// knows how.
func SetCorrelationHeaders(ctx *azugo.Context, header *fasthttp.RequestHeader) {
	if cid := correlation.ID(ctx); cid != "" {
		header.Set(correlation.HeaderCorrelationID, cid)
	}

	if iid := correlation.AppInstanceID(ctx); iid != "" {
		header.Set(propagation.HeaderAppInstanceID, iid)
	}
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
