package errors

import (
	"encoding/json"
	stderrors "errors"

	azugo "azugo.io/azugo"
	"azugo.io/core/http"
	"github.com/go-playground/validator/v10"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"

	"github.com/gmb-lib/go-platform-kit/correlation"
)

// Handler returns the router error handler that renders every error — this
// service's own or one relayed from downstream — as a uniform RFC 9457
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
func Handler(source string, public bool) func(*azugo.Context, error) bool {
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
			np := NewProblem(code)
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
