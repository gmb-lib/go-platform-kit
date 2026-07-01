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
		fasthttp.StatusNotFound:            "err:request:notFound",
		fasthttp.StatusForbidden:           "err:request:forbidden",
		fasthttp.StatusUnauthorized:        "err:request:unauthorized",
		fasthttp.StatusConflict:            "err:request:conflict",
		fasthttp.StatusBadGateway:          "err:upstream:unavailable",
		fasthttp.StatusServiceUnavailable:  "err:upstream:unavailable",
		fasthttp.StatusInternalServerError: "err:internal:unexpected",
		fasthttp.StatusTeapot:              "err:internal:unexpected",
	}

	for status, want := range cases {
		qt.Check(t, qt.Equals(genericCodeForStatus(status), want), qt.Commentf("status %d", status))
	}
}
