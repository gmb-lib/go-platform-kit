package errors

import (
	"encoding/json"
	stderrors "errors"
	"strings"
	"time"

	azugo "azugo.io/azugo"
	"azugo.io/core/http"
	"github.com/go-playground/validator/v10"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"

	"github.com/gmb-lib/go-platform-kit/correlation"
	"github.com/gmb-lib/go-platform-kit/propagation"
)

// HeaderAppInstanceID is the inbound header identifying the calling wallet
// app instance. It rides through to FailureEvent.AppInstanceID for audit.
const HeaderAppInstanceID = propagation.HeaderAppInstanceID

// FailureEvent is the audit-worthy record of an error response returned to
// a caller: which service produced it, at which endpoint, with what message,
// status, and calling app instance. Handler invokes FailureHook with this
// once per error, only at the service where the error originated - a relayed
// downstream error is not re-audited at every hop it passes through.
type FailureEvent struct {
	Service       string
	Endpoint      string
	ErrorMessage  string
	StatusCode    int
	AppInstanceID string
	OccurredAt    time.Time
}

// FailureHook is called by Handler for every error response this service
// itself originates, so a caller can persist an audit record. It must not
// panic and should swallow its own errors (log and continue) - it is a
// best-effort side write, never allowed to affect the response.
type FailureHook func(ctx *azugo.Context, evt FailureEvent)

// Handler returns the router error handler that renders every error - this
// service's own or one relayed from downstream - as a uniform RFC 9457
// application/problem+json response. Installed once (see platform.Setup) it
// replaces both the hand-rolled per-service error bodies and the framework's
// default {"errors":[…]} shape, so the wire has exactly one error format.
//
// source is stamped as Problem.Source (the service id) when an error does not
// already carry one. When public is true the response is projected to the
// public envelope — Source and Chain are dropped and Detail is withheld unless
// marked public-safe — which the single public-facing boundary service (the
// BFF) sets; internal services leave it false so service-to-service errors
// carry the full envelope for relay and logging.
//
// It returns true (the response is fully written) for every non-nil error;
// nil falls through to the framework.
func Handler(source string, public bool, hook FailureHook) func(*azugo.Context, error) bool {
	return func(ctx *azugo.Context, err error) bool {
		if err == nil {
			return false
		}

		p := toProblem(err)
		if p.Source == "" {
			p.Source = source
		}

		if p.TraceID == "" {
			p.TraceID = traceThread(ctx)
		}

		// One structured, correlated error record per rendered error — the
		// correlation id and trace id ride ctx.Log(). This is the uniform error
		// log: it fires even when the handler wrote none, so an error is always
		// joinable to its request by code + trace id.
		logProblem(ctx, p)

		// Audit only at the origin: p.Source names the service that produced the
		// error (set above, or preserved from a downstream Problem - see
		// Relay), so this is only true where the failure actually happened, not
		// at every hop that relays it onward. Without this check a single
		// downstream failure would be audited once per hop in the call chain.
		if hook != nil && p.Source == source {
			hook(ctx, FailureEvent{
				Service:  p.Source,
				Endpoint: ctx.RouterPath(),
				// p.Error() falls back to Title (always set) when Detail
				// (occurrence-specific, opt-in via WithDetail) is empty -
				// most errors never set Detail, so using Detail alone here
				// left error_message blank for the common case.
				ErrorMessage:  p.Error(),
				StatusCode:    p.StatusCode(),
				AppInstanceID: strings.TrimSpace(ctx.Header.Get(HeaderAppInstanceID)),
				OccurredAt:    time.Now(),
			})
		}

		ctx.StatusCode(p.StatusCode())

		// Preserve any framework error response headers (e.g. Retry-After).
		var rh azugo.ErrorHeaders
		if stderrors.As(err, &rh) {
			for name, value := range rh.ErrorHeaders() {
				ctx.Header.Set(name, value)
			}
		}

		var (
			body []byte
			mErr error
		)

		if public {
			body, mErr = json.Marshal(p.Public())
		} else {
			body, mErr = json.Marshal(p)
		}

		if mErr != nil {
			// Marshalling the envelope should never fail; if it does, fall back
			// to the framework's default formatting rather than send nothing.
			return false
		}

		ctx.ContentType(ContentTypeProblemJSON)
		ctx.Raw(body)

		return true
	}
}

// toProblem coerces any error into a Problem: an existing Problem (produced here
// or decoded from a relayed downstream error) is normalized and reused; an error
// carrying a stable code (Coder) becomes a coded Problem; any other error gets a
// uniform envelope derived from its HTTP status, so the wire shape is identical
// regardless of how the error was created.
func toProblem(err error) *Problem {
	var p *Problem
	if stderrors.As(err, &p) {
		return normalized(p)
	}

	var coder Coder
	if stderrors.As(err, &coder) {
		if code := coder.ErrorCode(); code != "" {
			opts := make([]ProblemOption, 0, 1)

			var rsc http.ResponseStatusCode
			if stderrors.As(err, &rsc) {
				// Guard against StatusCode() returning 0 (unset): WithStatus(0)
				// would overwrite a status NewProblem already derived correctly
				// from the taxonomy, and NewProblem's own zero-status fallback
				// would then downgrade a well-known code to a bare 500.
				if s := rsc.StatusCode(); s != 0 {
					opts = append(opts, WithStatus(s))
				}
			}

			np := NewProblem(code, opts...)
			if se, ok := err.(azugo.SafeError); ok {
				np.Detail = se.SafeError()
			}

			return np
		}
	}

	status := statusForError(err)
	np := &Problem{
		Status: status,
		Code:   genericCodeForStatus(status),
		Title:  titleForStatus(status),
	}

	if se, ok := err.(azugo.SafeError); ok {
		if msg := se.SafeError(); msg != "" {
			np.Detail = msg
		}
	}

	return np
}

// normalized fills any missing required field of a Problem so the wire envelope
// is always complete (status, title, code), whether it was built here or decoded
// from a downstream hop.
func normalized(p *Problem) *Problem {
	if p.Status == 0 {
		if p.Code != "" {
			p.Status = statusForCode(p.Code)
		} else {
			p.Status = fasthttp.StatusInternalServerError
		}
	}

	if p.Code == "" {
		p.Code = genericCodeForStatus(p.Status)
	}

	if p.Title == "" {
		if title, ok := titleForCodeOK(p.Code); ok {
			p.Title = title
		} else {
			p.Title = titleForStatus(p.Status)
		}
	}

	return p
}

// statusForError derives the HTTP status of a bare error the way the framework
// would: a validation error is 422, an error implementing the status interface
// uses its status, anything else is 500.
func statusForError(err error) int {
	var verr validator.ValidationErrors
	if stderrors.As(err, &verr) {
		return fasthttp.StatusUnprocessableEntity
	}

	var rsc http.ResponseStatusCode
	if stderrors.As(err, &rsc) {
		return rsc.StatusCode()
	}

	return fasthttp.StatusInternalServerError
}

// traceThread returns the id to echo as the response trace id: the active trace
// id when tracing is on (the key into the traces), else the correlation id
// (always present). Either is a valid key for finding the request in the logs.
func traceThread(ctx *azugo.Context) string {
	ids := correlation.FromContext(ctx)
	if ids.TraceID != "" {
		return ids.TraceID
	}

	return ids.CorrelationID
}

// logProblem emits the uniform error log line for a rendered error. Server
// errors (5xx) log at Error, client errors (4xx) at Warn, so a level>=error
// view surfaces only genuine failures. The correlation id, trace id, and span
// id are added by the correlation middleware (they ride ctx.Log()); this adds
// the error specifics. detail is a SafeError, so it never leaks to the logs.
func logProblem(ctx *azugo.Context, p *Problem) {
	fields := make([]zap.Field, 0, 4)
	fields = append(fields,
		zap.String("error.code", p.Code),
		zap.Int("http.response.status_code", p.Status),
	)
	if p.Source != "" {
		fields = append(fields, zap.String("error.source", p.Source))
	}
	if p.Detail != "" {
		fields = append(fields, zap.String("error.detail", p.Detail))
	}

	if p.Status >= fasthttp.StatusInternalServerError {
		ctx.Log().Error("request error", fields...)
	} else {
		ctx.Log().Warn("request error", fields...)
	}
}
