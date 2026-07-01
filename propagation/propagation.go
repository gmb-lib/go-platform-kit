// Package propagation is the dependency-free leaf that carries the correlation
// id across service boundaries on a context.Context.
//
// It exists so a library that only ever sees a context.Context — an
// on-behalf/background HTTP client that has no web-framework request object —
// can read and forward the correlation id without importing the web framework
// (which would drag its whole module graph in, or invert the dependency). This
// package imports only the standard library; both the request-side middleware
// and every outbound client depend on this one leaf, so the header name and the
// context key have a single home.
package propagation

import "context"

// HeaderCorrelationID is the HTTP header that carries the correlation id between
// services. It is the single source of truth for the header name: the inbound
// middleware echoes it, and every outbound client sets it.
const HeaderCorrelationID = "X-Correlation-ID"

// requestValueName is the per-request value name the inbound correlation
// middleware stores the id under, on frameworks whose request context resolves
// string-keyed values (fasthttp/azugo). Kept here so a reader holding only a
// context.Context and the request middleware agree on one name.
const requestValueName = "platform.correlation_id"

// RequestValueName returns the per-request value name the inbound correlation
// middleware stores the correlation id under. The middleware writes it there;
// readers that hold only a context.Context recover it via CorrelationID.
func RequestValueName() string { return requestValueName }

// correlationKeyType is the unexported type of the context key used by
// WithCorrelationID. A distinct type (not a bare string) means the key can
// never collide with a key defined in another package.
type correlationKeyType struct{}

var correlationKey correlationKeyType

// WithCorrelationID returns a copy of ctx carrying id as the correlation id.
//
// Use it for work that has no inbound HTTP request to inherit from — background
// jobs, schedulers, consumers — so an outbound call made from that context
// still propagates a stable id. A request handler does not need it: the inbound
// middleware already binds the id to the request context.
//
// An empty id is a no-op (returns ctx unchanged) so callers need not branch.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}

	return context.WithValue(ctx, correlationKey, id)
}

// CorrelationID returns the correlation id carried by ctx, or "" if none.
//
// It resolves both carriers so one accessor works everywhere:
//   - the context value set by WithCorrelationID (background contexts), and
//   - the request value the inbound middleware sets under RequestValueName()
//     (request contexts — reachable through the framework's context.Value chain
//     even when the caller holds the request only as a context.Context).
//
// This is what lets an on-behalf/background HTTP client read the id bound by the
// web request middleware without importing the framework.
func CorrelationID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}

	if v, ok := ctx.Value(correlationKey).(string); ok && v != "" {
		return v
	}

	if v, ok := ctx.Value(requestValueName).(string); ok {
		return v
	}

	return ""
}
