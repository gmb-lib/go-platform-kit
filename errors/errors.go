// Package errors is the project error taxonomy. It maps the DB layer's
// namespaced result codes (`err:domain:reason`, e.g. from
// result_error('err:document:notFound', …)) to Azugo HTTP error types and to
// safe client messages, so every service returns consistent HTTP status codes
// and never leaks internals.
//
// Auth-specific mappings stay in go-authbyte; this package covers the general
// domain/data errors. Where Azugo lacks a matching error
// type (e.g. 409 Conflict), this package supplies one that implements Azugo's
// ResponseStatusCode and SafeError interfaces so ctx.Error(err) maps it
// automatically.
package errors

import (
	"strings"

	azugo "azugo.io/azugo"
	"azugo.io/core/http"
	"github.com/valyala/fasthttp"
)

// Prefix is the namespace every DB result code carries.
const Prefix = "err"

// Code is a parsed DB result code of the form `err:domain:reason`.
type Code struct {
	// Domain is the resource/area, e.g. "document", "envelope", "identity".
	Domain string
	// Reason is the specific failure, e.g. "notFound", "forbidden", "conflict".
	Reason string
}

// ParseCode splits a namespaced result code (`err:domain:reason`) into its
// parts. ok is false if the value is not a recognized result code (missing the
// `err:` prefix). Extra colon-separated segments beyond domain are folded into
// the domain so codes like `err:document:slot:notFound` still resolve a reason.
func ParseCode(code string) (Code, bool) {
	parts := strings.Split(strings.TrimSpace(code), ":")
	if len(parts) < 3 || parts[0] != Prefix {
		return Code{}, false
	}

	reason := parts[len(parts)-1]
	domain := strings.Join(parts[1:len(parts)-1], ":")

	return Code{Domain: domain, Reason: reason}, true
}

// ConflictError reports a state conflict (HTTP 409). Azugo/core has no conflict
// type, so the taxonomy defines one here.
type ConflictError struct {
	// Resource is the conflicting resource (used in the safe message).
	Resource string
}

func (e ConflictError) Error() string {
	if e.Resource == "" {
		return "conflict"
	}

	return e.Resource + " conflict"
}

// SafeError returns a client-safe message.
func (e ConflictError) SafeError() string {
	return e.Error()
}

// StatusCode returns HTTP 409 Conflict.
func (ConflictError) StatusCode() int { return fasthttp.StatusConflict }

// GoneError reports a resource that existed but is no longer available (HTTP
// 410), e.g. an expired signing envelope.
type GoneError struct {
	Resource string
}

func (e GoneError) Error() string {
	if e.Resource == "" {
		return "no longer available"
	}

	return e.Resource + " no longer available"
}

// SafeError returns a client-safe message.
func (e GoneError) SafeError() string { return e.Error() }

// StatusCode returns HTTP 410 Gone.
func (GoneError) StatusCode() int { return fasthttp.StatusGone }

// InternalError is the safe fallback for an unmapped or genuinely internal
// failure: it returns HTTP 500 with a fixed, non-leaking message while keeping
// the underlying error for server-side logging.
type InternalError struct {
	Err error
}

func (e InternalError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}

	return "internal error"
}

// SafeError returns a fixed message that never exposes internals.
func (InternalError) SafeError() string { return "internal server error" }

// StatusCode returns HTTP 500 Internal Server Error.
func (InternalError) StatusCode() int { return fasthttp.StatusInternalServerError }

// Unwrap exposes the wrapped error for errors.Is/As and logging.
func (e InternalError) Unwrap() error { return e.Err }

// FromResultCode maps a DB namespaced result code to the Azugo HTTP error type
// the service should return. The optional safeMsg overrides the default
// client-safe message for the mapped error where the type carries one.
//
// Unrecognized codes map to InternalError (HTTP 500) with a fixed safe message,
// so an unmapped DB error can never leak its internals to the client.
func FromResultCode(code string, safeMsg ...string) error {
	parsed, ok := ParseCode(code)
	if !ok {
		return InternalError{Err: errString(code)}
	}

	return mapReason(parsed, msg(safeMsg))
}

// HTTP returns the Azugo HTTP error for a reason within a domain. It is the
// programmatic counterpart of FromResultCode for services that classify errors
// without a DB result code.
func HTTP(domain, reason string, safeMsg ...string) error {
	return mapReason(Code{Domain: domain, Reason: reason}, msg(safeMsg))
}

// mapReason resolves a parsed code to a concrete Azugo-mappable error. Reason
// matching is case-insensitive and normalizes common spelling variants.
func mapReason(c Code, safe string) error {
	switch normalize(c.Reason) {
	case "notfound", "missing", "unknown", "doesnotexist":
		return http.NotFoundError{Resource: resource(c.Domain, safe)}
	case "forbidden", "accessdenied", "denied", "notallowed", "notpermitted":
		return http.ForbiddenError{}
	case "unauthorized", "unauthenticated":
		return http.UnauthorizedError{}
	case "conflict", "alreadyexists", "duplicate", "exists":
		return ConflictError{Resource: resource(c.Domain, safe)}
	case "gone", "expired", "revoked":
		return GoneError{Resource: resource(c.Domain, safe)}
	case "invalid", "validation", "malformed", "badrequest", "badinput":
		return azugo.BadRequestError{Description: safe}
	case "required", "missingparameter", "missingfield":
		return azugo.ParamRequiredError{Name: c.Domain}
	default:
		// An unrecognized reason is treated as an internal failure: never leak
		// the raw code to the client.
		return InternalError{Err: errString(Prefix + ":" + c.Domain + ":" + c.Reason)}
	}
}

// normalize lowercases and strips separators so "not_found", "notFound" and
// "NOT-FOUND" all match the same case.
func normalize(s string) string {
	r := strings.NewReplacer("_", "", "-", "", " ", "")

	return r.Replace(strings.ToLower(strings.TrimSpace(s)))
}

// resource picks the resource label for an error message: the explicit safe
// message if provided, otherwise the domain.
func resource(domain, safe string) string {
	if safe != "" {
		return safe
	}

	return domain
}

func msg(safeMsg []string) string {
	if len(safeMsg) > 0 {
		return safeMsg[0]
	}

	return ""
}

// errString is a tiny error wrapper for carrying a string cause.
type errString string

func (e errString) Error() string { return string(e) }
