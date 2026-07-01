package errors

import (
	stderrors "errors"
	"testing"

	azugo "azugo.io/azugo"
	"github.com/go-playground/validator/v10"
	"github.com/go-quicktest/qt"
	"github.com/valyala/fasthttp"
)

// codedError is a stand-in for a service error that carries a stable code
// without being a Problem — the Coder path the renderer preserves.
type codedError struct {
	code string
	safe string
}

func (e codedError) Error() string     { return e.code }
func (e codedError) ErrorCode() string { return e.code }
func (e codedError) SafeError() string { return e.safe }

func TestToProblem_PassthroughNormalizes(t *testing.T) {
	// A Problem missing status/title (e.g. decoded from a sparse downstream body)
	// is completed, not discarded.
	p := toProblem(&Problem{Code: "err:document:notFound"})

	qt.Check(t, qt.Equals(p.Status, fasthttp.StatusNotFound))
	qt.Check(t, qt.Equals(p.Title, "Not found"))
}

func TestToProblem_CoderKeepsCodeAndDetail(t *testing.T) {
	p := toProblem(codedError{code: "err:envelope:conflict", safe: "already signed"})

	qt.Check(t, qt.Equals(p.Code, "err:envelope:conflict"))
	qt.Check(t, qt.Equals(p.Status, fasthttp.StatusConflict))
	qt.Check(t, qt.Equals(p.Detail, "already signed"))
}

func TestToProblem_BareErrorIsUniform500(t *testing.T) {
	p := toProblem(stderrors.New("boom"))

	qt.Check(t, qt.Equals(p.Status, fasthttp.StatusInternalServerError))
	qt.Check(t, qt.Equals(p.Code, "err:internal:unexpected"))
	qt.Check(t, qt.Equals(p.Title, "Internal server error"))
	// A bare error exposes no SafeError, so no detail leaks.
	qt.Check(t, qt.Equals(p.Detail, ""))
}

func TestToProblem_FrameworkErrorUsesItsStatusAndSafeDetail(t *testing.T) {
	p := toProblem(azugo.BadRequestError{Description: "malformed body"})

	qt.Check(t, qt.Equals(p.Status, fasthttp.StatusBadRequest))
	qt.Check(t, qt.Equals(p.Code, "err:request:invalid"))
	qt.Check(t, qt.Equals(p.Detail, "malformed body"))
}

func TestToProblem_NormalizesEmptyProblem(t *testing.T) {
	// A Problem with neither status nor code (e.g. a zero-value one built by
	// hand) still normalizes to the safe, uniform 500 shape.
	p := toProblem(&Problem{})

	qt.Check(t, qt.Equals(p.Status, fasthttp.StatusInternalServerError))
	qt.Check(t, qt.Equals(p.Code, "err:internal:unexpected"))
	qt.Check(t, qt.Equals(p.Title, "Internal server error"))
}

func TestStatusForError_ValidationIs422(t *testing.T) {
	type payload struct {
		Name string `validate:"required"`
	}

	verr := validator.New().Struct(payload{})
	qt.Assert(t, qt.IsNotNil(verr))

	qt.Check(t, qt.Equals(statusForError(verr), fasthttp.StatusUnprocessableEntity))
}

func TestStatusForError_Fallbacks(t *testing.T) {
	qt.Check(t, qt.Equals(statusForError(stderrors.New("x")), fasthttp.StatusInternalServerError))
	qt.Check(t, qt.Equals(statusForError(azugo.ParamInvalidError{Name: "n"}), fasthttp.StatusUnprocessableEntity))
}

func TestGenericCodeForStatus(t *testing.T) {
	cases := map[int]string{
		fasthttp.StatusBadRequest:            "err:request:invalid",
		fasthttp.StatusUnauthorized:          "err:request:unauthorized",
		fasthttp.StatusForbidden:             "err:request:forbidden",
		fasthttp.StatusNotFound:              "err:request:notFound",
		fasthttp.StatusConflict:              "err:request:conflict",
		fasthttp.StatusGone:                  "err:request:gone",
		fasthttp.StatusRequestEntityTooLarge: "err:request:payloadTooLarge",
		fasthttp.StatusUnsupportedMediaType:  "err:request:unsupportedMediaType",
		fasthttp.StatusUnprocessableEntity:   "err:request:unprocessable",
		fasthttp.StatusTooManyRequests:       "err:request:rateLimited",
		fasthttp.StatusNotImplemented:        "err:internal:notImplemented",
		fasthttp.StatusBadGateway:            "err:upstream:unavailable",
		fasthttp.StatusServiceUnavailable:    "err:upstream:unavailable",
		fasthttp.StatusGatewayTimeout:        "err:upstream:unavailable",
		fasthttp.StatusInternalServerError:   "err:internal:unexpected",
		fasthttp.StatusTeapot:                "err:internal:unexpected",
	}

	for status, want := range cases {
		qt.Check(t, qt.Equals(genericCodeForStatus(status), want), qt.Commentf("status %d", status))
	}
}

func TestTitleForStatus_CoversEveryStandardStatus(t *testing.T) {
	// title-from-status must hold for every standard status a service can
	// return, so an emitter never has to override the title by hand just
	// because the reason falls outside the taxonomy table.
	cases := map[int]string{
		fasthttp.StatusBadRequest:            "Bad request",
		fasthttp.StatusUnauthorized:          "Unauthorized",
		fasthttp.StatusForbidden:             "Forbidden",
		fasthttp.StatusNotFound:              "Not found",
		fasthttp.StatusConflict:              "Conflict",
		fasthttp.StatusGone:                  "No longer available",
		fasthttp.StatusRequestEntityTooLarge: "Payload too large",
		fasthttp.StatusUnsupportedMediaType:  "Unsupported media type",
		fasthttp.StatusUnprocessableEntity:   "Unprocessable entity",
		fasthttp.StatusTooManyRequests:       "Too many requests",
		fasthttp.StatusNotImplemented:        "Not implemented",
		fasthttp.StatusBadGateway:            "Upstream unavailable",
		fasthttp.StatusServiceUnavailable:    "Service unavailable",
		fasthttp.StatusGatewayTimeout:        "Upstream timeout",
		fasthttp.StatusTeapot:                "Internal server error",
	}

	for status, want := range cases {
		qt.Check(t, qt.Equals(titleForStatus(status), want), qt.Commentf("status %d", status))
	}
}
