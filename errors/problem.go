package errors

import (
	"encoding/json"

	azugo "azugo.io/azugo"
	"github.com/valyala/fasthttp"
)

// ContentTypeProblemJSON is the media type of the error envelope (RFC 9457).
const ContentTypeProblemJSON = "application/problem+json"

// MaxChainHops bounds the internal chain breadcrumb. A relay that would exceed
// it keeps the root hop (where the error originated) and the most recent hops,
// collapsing the elided middle into a single marker (see boundChain). The full
// path is always reconstructable from the logs by the trace id, so bounding
// costs nothing and keeps any single log line and internal response sane.
const MaxChainHops = 8

// Problem is the uniform error envelope every service returns (RFC 9457
// Problem Details, house profile). It is produced for this service's own
// errors and preserved when relaying a downstream error, so the terminal
// code/source/trace id survive the whole trip back to the initiator.
//
// It is a Go error: it implements the status, safe-message, and custom-body
// interfaces the web framework consults, so ctx.Error(problem) renders it as
// application/problem+json with the right HTTP status. The global renderer
// (see Handler) makes this the shape of every error response, produced or not.
type Problem struct {
	// Type is an optional URI identifying the problem kind. Defaults to
	// about:blank (omitted) until a documentation registry exists.
	Type string `json:"type,omitempty"`
	// Title is the stable, human-readable summary of the problem kind. It is
	// the string a public client displays; it is carried from the origin, never
	// authored at a relay.
	Title string `json:"title"`
	// Status mirrors the HTTP status line.
	Status int `json:"status"`
	// Detail is the occurrence-specific message. It is a SafeError (never a
	// secret, PII, or internal name) but is internal-by-default: dropped at the
	// public boundary unless the emitter marked it public-safe.
	Detail string `json:"detail,omitempty"`
	// Code is the stable machine identifier the caller branches on
	// (err:domain:reason). The middle segment is the domain, not the service.
	Code string `json:"code"`
	// Source is the id of the service that originated the error. Internal only:
	// stripped at the public boundary.
	Source string `json:"source,omitempty"`
	// TraceID is the thread that pulls every related log line. Safe to expose;
	// it is the operator's jump-off point into the logs and traces.
	TraceID string `json:"trace_id,omitempty"`
	// Chain is the ordered breadcrumb of hops, root (origin) first. Internal
	// only: stripped at the public boundary. Bounded by MaxChainHops.
	Chain []Hop `json:"chain,omitempty"`

	// detailPublic records that Detail is safe to expose at the public boundary.
	// It is set only by the emitter (WithPublicDetail); a Detail decoded from a
	// downstream hop is treated as internal.
	detailPublic bool
}

// Hop is one step in a Problem's chain: the service that handled the error and
// the code/status it saw or returned. An elided-middle marker carries only
// Elided (the count of omitted hops).
type Hop struct {
	Service string `json:"service,omitempty"`
	Code    string `json:"code,omitempty"`
	Status  int    `json:"status,omitempty"`
	// Elided is the number of middle hops omitted by bounding (see boundChain).
	// Present only on the single marker hop; zero (omitted) on real hops.
	Elided int `json:"elided,omitempty"`
}

// PublicProblem is the boundary projection of a Problem: it structurally omits
// Source and Chain, so a public response cannot leak service topology no matter
// how it is built. The public boundary renders this shape (see Problem.Public);
// the strip is a property of the type, not a field a caller must remember to
// clear.
type PublicProblem struct {
	Type    string `json:"type,omitempty"`
	Title   string `json:"title"`
	Status  int    `json:"status"`
	Detail  string `json:"detail,omitempty"`
	Code    string `json:"code"`
	TraceID string `json:"trace_id,omitempty"`
}

// Coder is implemented by an error that carries a stable err:domain:reason
// code. A service can attach a code to its own error type and the renderer will
// preserve it; a Problem is matched directly and need not implement this.
type Coder interface {
	ErrorCode() string
}

// Error implements error. It is the internal (log-side) representation: the
// code and the most specific message available. It is never the client body —
// that is the JSON envelope.
func (p *Problem) Error() string {
	msg := p.Detail
	if msg == "" {
		msg = p.Title
	}

	if p.Code != "" {
		if msg == "" {
			return p.Code
		}

		return p.Code + ": " + msg
	}

	return msg
}

// SafeError implements azugo.SafeError: the client-safe message is the stable
// title, never the occurrence detail.
func (p *Problem) SafeError() string { return p.Title }

// StatusCode implements the framework's status interface so ctx.Error(problem)
// returns the right HTTP status.
func (p *Problem) StatusCode() int {
	if p.Status != 0 {
		return p.Status
	}

	return fasthttp.StatusInternalServerError
}

// MarshalError implements azugo.ErrorMarshaler so a Problem renders its own
// application/problem+json body. It renders for JSON (and problem+json)
// negotiation and falls back (ok=false) for anything else (e.g. XML), letting
// the framework format it. The global Handler renders every error uniformly;
// this keeps a Problem self-describing where the Handler is not installed.
func (p *Problem) MarshalError(contentType string) (body []byte, ct string, ok bool) {
	if contentType != azugo.ContentTypeJSON && contentType != ContentTypeProblemJSON {
		return nil, "", false
	}

	b, err := json.Marshal(p)
	if err != nil {
		return nil, "", false
	}

	return b, ContentTypeProblemJSON, true
}

// Public returns the boundary projection: Source and Chain are dropped
// structurally, and Detail is included only if the emitter marked it public-safe.
func (p *Problem) Public() *PublicProblem {
	pp := &PublicProblem{
		Type:    p.Type,
		Title:   p.Title,
		Status:  p.Status,
		Code:    p.Code,
		TraceID: p.TraceID,
	}

	if p.detailPublic {
		pp.Detail = p.Detail
	}

	return pp
}

// ProblemOption configures a Problem at construction.
type ProblemOption func(*Problem)

// WithDetail sets the occurrence-specific, internal-by-default message. It must
// be a SafeError (no secrets/PII/internal names); it is dropped at the public
// boundary.
func WithDetail(detail string) ProblemOption {
	return func(p *Problem) { p.Detail = detail }
}

// WithPublicDetail sets a detail that is also safe to show a public client
// (it survives the boundary projection). Use only for messages deliberately
// meant for end users.
func WithPublicDetail(detail string) ProblemOption {
	return func(p *Problem) {
		p.Detail = detail
		p.detailPublic = true
	}
}

// WithStatus overrides the HTTP status derived from the code.
func WithStatus(status int) ProblemOption {
	return func(p *Problem) { p.Status = status }
}

// WithTitle overrides the stable title derived from the code.
func WithTitle(title string) ProblemOption {
	return func(p *Problem) { p.Title = title }
}

// WithSource sets the originating service id. The renderer fills it from the
// app name when unset, so a handler rarely needs this.
func WithSource(source string) ProblemOption {
	return func(p *Problem) { p.Source = source }
}

// WithType sets the problem type URI.
func WithType(typeURI string) ProblemOption {
	return func(p *Problem) { p.Type = typeURI }
}

// NewProblem builds a Problem for a stable code (err:domain:reason). The HTTP
// status and title are derived from the code's reason via the taxonomy — the
// single source of truth for status — and can be overridden with options.
func NewProblem(code string, opts ...ProblemOption) *Problem {
	p := &Problem{
		Code:   code,
		Status: statusForCode(code),
	}
	// Derive a title only for a recognized taxonomy reason; otherwise leave it
	// empty so it follows the final (possibly overridden) status below, rather
	// than misleadingly defaulting to the 500 title for a domain-specific reason.
	if title, ok := titleForCodeOK(code); ok {
		p.Title = title
	}

	for _, opt := range opts {
		opt(p)
	}

	if p.Status == 0 {
		p.Status = fasthttp.StatusInternalServerError
	}

	if p.Title == "" {
		p.Title = titleForStatus(p.Status)
	}

	return p
}

// statusForCode derives the HTTP status for a code by reusing the taxonomy: the
// mapped error's status is the single source of truth. An unrecognized code
// maps to 500, exactly as FromResultCode does.
func statusForCode(code string) int {
	if sc, ok := FromResultCode(code).(interface{ StatusCode() int }); ok {
		return sc.StatusCode()
	}

	return fasthttp.StatusInternalServerError
}

// titleForCodeOK derives the stable title from a code's reason. ok is false when
// the code is unparseable or its reason is not a recognized taxonomy reason, so
// the caller falls back to a status-derived title instead of a misleading one
// (e.g. a domain-specific reason such as legalHold carried with an overridden
// status).
func titleForCodeOK(code string) (string, bool) {
	c, ok := ParseCode(code)
	if !ok {
		return "", false
	}

	return titleForReasonOK(c.Reason)
}

// titleForReasonOK maps a taxonomy reason to a stable, human-readable title,
// using the same normalization as the status mapping so the two never diverge.
// ok is false for an unrecognized reason (the title then follows the status).
func titleForReasonOK(reason string) (string, bool) {
	switch normalize(reason) {
	case "notfound", "missing", "unknown", "doesnotexist":
		return "Not found", true
	case "forbidden", "accessdenied", "denied", "notallowed", "notpermitted":
		return "Forbidden", true
	case "unauthorized", "unauthenticated":
		return "Unauthorized", true
	case "conflict", "alreadyexists", "duplicate", "exists":
		return "Conflict", true
	case "gone", "expired", "revoked":
		return "No longer available", true
	case "invalid", "validation", "malformed", "badrequest", "badinput":
		return "Invalid request", true
	case "required", "missingparameter", "missingfield":
		return "Missing required parameter", true
	default:
		return "", false
	}
}

// titleForStatus is the fallback title when only a status is known (an error
// that carries no code).
func titleForStatus(status int) string {
	switch status {
	case fasthttp.StatusBadRequest:
		return "Bad request"
	case fasthttp.StatusUnauthorized:
		return "Unauthorized"
	case fasthttp.StatusForbidden:
		return "Forbidden"
	case fasthttp.StatusNotFound:
		return "Not found"
	case fasthttp.StatusConflict:
		return "Conflict"
	case fasthttp.StatusGone:
		return "No longer available"
	case fasthttp.StatusUnprocessableEntity:
		return "Unprocessable entity"
	case fasthttp.StatusTooManyRequests:
		return "Too many requests"
	case fasthttp.StatusBadGateway:
		return "Upstream unavailable"
	case fasthttp.StatusServiceUnavailable:
		return "Service unavailable"
	case fasthttp.StatusGatewayTimeout:
		return "Upstream timeout"
	default:
		return "Internal server error"
	}
}

// genericCodeForStatus is the code assigned to an error that reaches the
// renderer without one (a bare framework error). It keeps the envelope uniform;
// a service that wants a precise domain code produces a Problem (or implements
// Coder) rather than relying on this fallback.
func genericCodeForStatus(status int) string {
	switch status {
	case fasthttp.StatusBadRequest:
		return "err:request:invalid"
	case fasthttp.StatusUnauthorized:
		return "err:request:unauthorized"
	case fasthttp.StatusForbidden:
		return "err:request:forbidden"
	case fasthttp.StatusNotFound:
		return "err:request:notFound"
	case fasthttp.StatusConflict:
		return "err:request:conflict"
	case fasthttp.StatusGone:
		return "err:request:gone"
	case fasthttp.StatusUnprocessableEntity:
		return "err:request:unprocessable"
	case fasthttp.StatusTooManyRequests:
		return "err:request:rateLimited"
	case fasthttp.StatusBadGateway, fasthttp.StatusServiceUnavailable, fasthttp.StatusGatewayTimeout:
		return "err:upstream:unavailable"
	default:
		return "err:internal:unexpected"
	}
}

// ParseProblem decodes an application/problem+json body into a *Problem. ok is
// false when the body is not a problem document (no code, title, or status),
// so a caller can fall back to a generic envelope for a non-conforming upstream.
func ParseProblem(body []byte) (*Problem, bool) {
	if len(body) == 0 {
		return nil, false
	}

	var p Problem
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, false
	}

	if p.Code == "" && p.Title == "" && p.Status == 0 {
		return nil, false
	}

	return &p, true
}

// Relay builds the error this service returns for a failed downstream call. It
// preserves the terminal code/source/trace id/title/detail (so the initiator
// still sees what actually failed) and appends this service to the chain,
// choosing outerStatus deliberately — never collapsing a parsed downstream
// error into a bare 502. down is the decoded downstream Problem; service is
// this service's id; outerStatus is the status this service returns (pass the
// downstream status to relay it unchanged, or a dependency status such as 424).
func Relay(down *Problem, service string, outerStatus int) *Problem {
	if down == nil {
		return NewProblem("err:upstream:unavailable", WithStatus(outerStatus), WithSource(service))
	}

	p := *down
	p.Status = outerStatus

	// Seed the chain with the origin hop when the downstream did not carry one,
	// so the root (where it failed) is always present.
	chain := down.Chain
	if len(chain) == 0 {
		chain = []Hop{{Service: down.Source, Code: down.Code, Status: down.Status}}
	}

	chain = append(chain[:len(chain):len(chain)], Hop{Service: service, Code: down.Code, Status: outerStatus})
	p.Chain = boundChain(chain)

	return &p
}

// boundChain caps a chain at MaxChainHops, always keeping the root hop (index 0,
// where the error originated) and the most recent hops, and collapsing the
// elided middle into a single {elided:N} marker.
func boundChain(chain []Hop) []Hop {
	if len(chain) <= MaxChainHops {
		return chain
	}

	// Reserve one slot for the root and one for the elided marker; the rest are
	// the most recent hops.
	tail := MaxChainHops - 2
	elided := len(chain) - 1 - tail

	out := make([]Hop, 0, MaxChainHops)
	out = append(out, chain[0])
	out = append(out, Hop{Elided: elided})
	out = append(out, chain[len(chain)-tail:]...)

	return out
}
