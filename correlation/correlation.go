// Package correlation owns the project correlation model — the single concern
// no upstream library can own.
//
// On every inbound request the Middleware reads (or issues) a correlation_id,
// adopts the OpenTelemetry trace_id/span_id from azugo.io/opentelemetry, binds
// all three to *azugo.Context, and ensures they appear on every log line. The
// same ids are then propagated on outbound HTTP (package httpclient), stamped
// onto every broker event (package broker), and copied into the audit event
// envelope by the audit emitter libraries — so one incident can be reconstructed
// end-to-end across logs, traces, and all three audit regimes by a single id.
package correlation

import (
	"strings"

	"azugo.io/azugo"
	"azugo.io/opentelemetry"
	"github.com/oklog/ulid/v2"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/gmb-lib/go-platform-kit/propagation"
)

// Header names carrying correlation across HTTP hops. The W3C `traceparent`
// header is injected/extracted by azugo.io/opentelemetry; correlation_id is ours.
const (
	// HeaderCorrelationID is the inbound/outbound correlation id header. It is
	// defined by the dependency-free propagation leaf so the request middleware
	// and every outbound client (including the on-behalf/background client that
	// cannot import this package) share one name.
	HeaderCorrelationID = propagation.HeaderCorrelationID

	// HeaderAppInstanceID is the inbound calling-app-instance header. Unlike
	// the correlation id it is never minted locally - an absent header simply
	// means no app instance id is bound to the request.
	HeaderAppInstanceID = propagation.HeaderAppInstanceID
)

// Log field keys. These are the canonical names that appear in every log line
// and in the audit event envelope.
const (
	LogKeyCorrelationID = "correlation_id"
	LogKeyTraceID       = "trace_id"
	LogKeySpanID        = "span_id"
	LogKeyAppInstanceID = "app_instance_id"
)

// MaxIDLength bounds an accepted inbound correlation id. Longer (or otherwise
// invalid) inbound values are ignored and a fresh id is used instead — the
// correlation id rides every log line, audit envelope, and outbound call, so
// it must never be an attacker-controlled free-text channel.
const MaxIDLength = 128

// IDs is the correlation triple carried on a request: the project correlation_id
// plus the OpenTelemetry trace_id/span_id (empty when tracing is inactive).
type IDs struct {
	CorrelationID string
	TraceID       string
	SpanID        string
}

// Middleware resolves the correlation triple for each inbound request and binds
// it to the context and the request logger.
//
// It must run after the azugo.io/opentelemetry middleware (so the active span is
// available) — platform.Setup guarantees this ordering by enabling tracing
// before installing this middleware.
//
// What it puts where, so a service adopting it knows what starts appearing in
// its telemetry — none of this is individually opt-out:
//
//   - correlation_id on every log line, echoed as a response header, and set as
//     an attribute on the request's own span;
//   - trace_id/span_id on every log line, when tracing is active;
//   - app_instance_id on every log line and as a span attribute, but only when
//     the inbound request carried a valid X-App-Instance-Id header. With no
//     caller sending that header the behaviour is invisible.
//
// Skipping the middleware entirely is the only way to opt out, and it costs the
// correlation model; a service that does not install it also reports an empty
// app instance id to errors.FailureHook, which is the correct fail-closed
// behaviour rather than a bug.
func Middleware() azugo.RequestHandlerFunc {
	return func(next azugo.RequestHandler) azugo.RequestHandler {
		return func(ctx *azugo.Context) {
			// 1. Read an inbound correlation id — accepted only when it passes
			// validation (bounded length, safe charset), since it is stamped
			// on every log line and audit envelope and echoed downstream. With
			// none (or an invalid one), adopt Azugo's own per-request id
			// (ctx.ID(), a ULID) rather than mint a parallel one — so the
			// access log's http.request.id and the correlation_id on every
			// other line share a single value. newID() is only a defensive
			// fallback should no request id be set.
			cid := strings.TrimSpace(ctx.Header.Get(HeaderCorrelationID))
			if !ValidID(cid) {
				cid = ""
			}
			if cid == "" {
				cid = ctx.ID()
			}
			if cid == "" {
				cid = newID()
			}

			ctx.SetUserValue(propagation.RequestValueName(), cid)
			// Echo it back so callers can correlate the response. SetAlways (not
			// Set) so it survives the response reset the framework performs when
			// rendering an error — otherwise error responses, the ones a caller
			// most wants to correlate, would drop the header.
			ctx.Header.SetAlways(HeaderCorrelationID, cid)

			// 2. Ensure all available ids appear on every subsequent log line.
			fields := make([]zap.Field, 0, 4)
			fields = append(fields, zap.String(LogKeyCorrelationID, cid))

			if tid, sid, ok := traceIDs(ctx); ok {
				fields = append(fields,
					zap.String(LogKeyTraceID, tid),
					zap.String(LogKeySpanID, sid),
				)
			}

			// 3. App instance id: forwarded, never minted - validated the same
			// way as the correlation id, since it rides the same log lines,
			// span, and (via errors.FailureHook) an audit envelope. An invalid
			// inbound value is simply no value, same as an absent header -
			// bind it (for AppInstanceID(ctx) and outbound propagation) and put
			// it on every log line so it's searchable without digging into a
			// specific outbound call's headers.
			iid := strings.TrimSpace(ctx.Header.Get(HeaderAppInstanceID))
			if !ValidID(iid) {
				iid = ""
			}
			if iid != "" {
				ctx.SetUserValue(propagation.AppInstanceRequestValueName(), iid)
				fields = append(fields, zap.String(LogKeyAppInstanceID, iid))
			}

			_ = ctx.AddLogFields(fields...)

			// 4. Stamp both ids on the root span itself - Azugo's server-span
			// builder does not capture arbitrary request headers (only the
			// outbound http-client instrumentation does), so without this an
			// id is visible only on whichever downstream call happens to carry
			// it as a header, never on the request's own top-level span.
			span := trace.SpanFromContext(opentelemetry.FromContext(ctx))
			if span.IsRecording() {
				span.SetAttributes(attribute.String(LogKeyCorrelationID, cid))
				if iid != "" {
					span.SetAttributes(attribute.String(LogKeyAppInstanceID, iid))
				}
			}

			next(ctx)
		}
	}
}

// FromContext returns the correlation triple bound to the request. The
// correlation_id is always present once Middleware has run; trace_id/span_id are
// present only when tracing is active for the request.
func FromContext(ctx *azugo.Context) IDs {
	ids := IDs{CorrelationID: ID(ctx)}

	if tid, sid, ok := traceIDs(ctx); ok {
		ids.TraceID = tid
		ids.SpanID = sid
	}

	return ids
}

// ID returns the correlation id bound to the request, or "" if Middleware has
// not run.
func ID(ctx *azugo.Context) string {
	if v, ok := ctx.UserValue(propagation.RequestValueName()).(string); ok {
		return v
	}

	return ""
}

// AppInstanceID returns the calling app instance id bound to the request, or
// "" if Middleware has not run, no X-App-Instance-Id header was present, or
// the header failed validation. Delegates to propagation.AppInstanceID so
// this accessor and a context-only reader (see propagation.AppInstanceID)
// resolve the identical bound value.
func AppInstanceID(ctx *azugo.Context) string {
	return propagation.AppInstanceID(ctx)
}

// traceIDs extracts the active OpenTelemetry trace_id/span_id from the request,
// returning ok=false when tracing is inactive (the span context is invalid).
func traceIDs(ctx *azugo.Context) (traceID, spanID string, ok bool) {
	sc := trace.SpanContextFromContext(opentelemetry.FromContext(ctx))
	if !sc.IsValid() {
		return "", "", false
	}

	return sc.TraceID().String(), sc.SpanID().String(), true
}

// newID mints a new ULID for use as a correlation id.
func newID() string {
	return ulid.Make().String()
}

// ValidID reports whether s is acceptable as an inbound correlation id: 1 to
// MaxIDLength characters from [A-Za-z0-9._-]. Anything else (empty, oversized,
// exotic characters, header-injection attempts) is rejected and replaced by a
// locally minted id.
func ValidID(s string) bool {
	if s == "" || len(s) > MaxIDLength {
		return false
	}

	for i := 0; i < len(s); i++ {
		c := s[i]

		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '.' || c == '_' || c == '-':
		default:
			return false
		}
	}

	return true
}
