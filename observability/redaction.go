// Package observability standardizes how every service turns on Azugo's own
// telemetry — logging (with PII/secret redaction), metrics naming, and
// OpenTelemetry tracing. It re-implements none of it: it configures and wraps
// Azugo's zap logger, the VictoriaMetrics/metrics registry, and
// azugo.io/opentelemetry so the whole fleet emits the same shapes.
package observability

import (
	"strings"

	"azugo.io/azugo"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// maskValue is the placeholder substituted for masked field values.
const maskValue = "[REDACTED]"

// RedactionPolicy decides, by log-field key, what must never reach the log sink.
// Matching is case-insensitive substring matching against the field key.
//
// - DropKeys: the whole field is omitted (credentials, secrets, document
// bytes — things that must not be stored at all).
// - MaskKeys: the field is kept but its value is replaced with "[REDACTED]"
// (free-text PII — useful to know a field was present without storing it).
//
// Redaction is applied centrally so a handler using ctx.Log() cannot
// accidentally log a token or document content.
//
// Limitation: matching is by TOP-LEVEL field key only. A nested value logged
// via zap.Any / zap.Reflect (e.g. a whole attributes map) is NOT inspected —
// sensitive keys inside it bypass redaction. Do not log raw maps/structs that
// may carry secrets or PII; log individual fields, or sanitize the map first
// (the audit emitter libraries do this for their own attribute maps).
type RedactionPolicy struct {
	DropKeys []string
	MaskKeys []string

	// dedupeKeys are exact field keys that are managed centrally and must appear
	// exactly once. They are dropped when re-applied via With, which neutralizes
	// the duplicate base-field set that App.ReplaceLogger re-adds (see
	// EnableRedaction). Populated by DefaultRedactionPolicy.
	dedupeKeys map[string]struct{}
}

// DefaultRedactionPolicy returns the fleet-standard policy: drop credentials,
// secrets, and document content; mask free-text PII. The lists are intentionally
// broad (substring matches) so that, e.g., "authorization", "dpop_proof", and
// "service_token" are all dropped.
func DefaultRedactionPolicy() *RedactionPolicy {
	return &RedactionPolicy{
		DropKeys: []string{
			"authorization", "dpop", "proof", "token", "secret", "password",
			"passwd", "credential", "cookie", "api_key", "apikey", "private_key",
			"privatekey", "client_secret", "session", "bearer",
			// Document content / large payloads must never be logged.
			"file_data", "filedata", "document_content", "documentcontent",
			"file_bytes", "payload", "raw_body", "rawbody",
		},
		MaskKeys: []string{
			"email", "phone", "msisdn", "personal_code", "personalcode",
			"national_id", "nationalid", "ssn", "given_name", "givenname",
			"family_name", "familyname", "full_name", "fullname", "birth",
			"dob", "address", "pii",
		},
		dedupeKeys: map[string]struct{}{
			// Standard service-identity fields Azugo bakes into the base logger
			// (azugo.io/core App.loggerFields). They are centrally owned, so the
			// duplicate copy re-added by ReplaceLogger is dropped.
			"service.name":         {},
			"service.version":      {},
			"service.environment":  {},
			"host.hostname":        {},
			"container.id":         {},
			"kubernetes.namespace": {},
			"kubernetes.pod.name":  {},
			"kubernetes.pod.uid":   {},
			"kubernetes.node.name": {},
		},
	}
}

// EnableRedaction installs the redacting policy on the application logger. Every
// log line — from ctx.Log() in handlers to the framework's own logs — is then
// scrubbed before it reaches the encoder. Redaction is not optional once
// installed.
//
// It wraps the core of the current application logger and re-sets it via
// App.ReplaceLogger. Because ReplaceLogger re-adds Azugo's standard
// service-identity fields, the policy drops those (already-present) keys when
// they are re-applied, so they appear exactly once. Call this from
// platform.Setup, before any request is served.
func EnableRedaction(app *azugo.App, policy *RedactionPolicy) {
	if policy == nil {
		policy = DefaultRedactionPolicy()
	}

	redacted := app.Log().WithOptions(zap.WrapCore(func(c zapcore.Core) zapcore.Core {
		return &redactCore{Core: c, policy: policy}
	}))

	_ = app.ReplaceLogger(redacted)
}

// redactCore is a zapcore.Core decorator that scrubs fields per the policy
// before delegating to the wrapped core. It scrubs on both With (fields baked
// into the logger, e.g. context fields and ReplaceLogger's base fields) and
// Write (per-call fields), so no path bypasses redaction.
type redactCore struct {
	zapcore.Core

	policy *RedactionPolicy
}

// With scrubs the baked-in fields and returns a decorator over the wrapped
// core's With.
func (c *redactCore) With(fields []zapcore.Field) zapcore.Core {
	return &redactCore{
		Core:   c.Core.With(c.policy.scrub(fields)),
		policy: c.policy,
	}
}

// Check routes the entry through this decorator (not the wrapped core) so Write
// — and therefore redaction — is always invoked.
func (c *redactCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(ent.Level) {
		return ce.AddCore(ent, c)
	}

	return ce
}

// Write scrubs the per-call fields before writing.
func (c *redactCore) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	return c.Core.Write(ent, c.policy.scrub(fields))
}

// scrub applies the policy to a field slice, returning a new slice with
// sensitive fields dropped or masked and centrally-owned duplicate fields
// removed.
func (p *RedactionPolicy) scrub(fields []zapcore.Field) []zapcore.Field {
	if len(fields) == 0 {
		return fields
	}

	out := make([]zapcore.Field, 0, len(fields))

	for _, f := range fields {
		if _, dup := p.dedupeKeys[f.Key]; dup {
			// Centrally-owned base field already present once on the inner core.
			continue
		}

		key := strings.ToLower(f.Key)

		switch {
		case containsAny(key, p.DropKeys):
			// Drop entirely.
			continue
		case containsAny(key, p.MaskKeys):
			out = append(out, zap.String(f.Key, maskValue))
		default:
			out = append(out, f)
		}
	}

	return out
}

// containsAny reports whether key contains any of the (lower-cased) substrings.
func containsAny(key string, subs []string) bool {
	for _, s := range subs {
		if strings.Contains(key, s) {
			return true
		}
	}

	return false
}
