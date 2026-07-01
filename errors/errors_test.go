package errors_test

import (
	stderrors "errors"
	"testing"

	"github.com/go-quicktest/qt"
	"github.com/valyala/fasthttp"

	pkerrors "github.com/gmb-lib/go-platform-kit/errors"
)

// statusCoder mirrors Azugo's ResponseStatusCode interface so tests can assert
// the HTTP status the framework would derive from a mapped error.
type statusCoder interface{ StatusCode() int }

// safeErrorer mirrors Azugo's SafeError interface.
type safeErrorer interface{ SafeError() string }

func TestParseCode(t *testing.T) {
	cases := []struct {
		in     string
		ok     bool
		domain string
		reason string
	}{
		{"err:document:notFound", true, "document", "notFound"},
		{"err:identity:forbidden", true, "identity", "forbidden"},
		{"err:document:slot:notFound", true, "document:slot", "notFound"},
		{"document:notFound", false, "", ""},
		{"err:document", false, "", ""},
		{"", false, "", ""},
	}

	for _, c := range cases {
		got, ok := pkerrors.ParseCode(c.in)
		qt.Check(t, qt.Equals(ok, c.ok), qt.Commentf("ParseCode(%q) ok", c.in))

		if c.ok {
			qt.Check(t, qt.Equals(got.Domain, c.domain), qt.Commentf("domain for %q", c.in))
			qt.Check(t, qt.Equals(got.Reason, c.reason), qt.Commentf("reason for %q", c.in))
		}
	}
}

func TestFromResultCode_StatusCodes(t *testing.T) {
	cases := []struct {
		code   string
		status int
	}{
		{"err:document:notFound", fasthttp.StatusNotFound},
		{"err:document:not_found", fasthttp.StatusNotFound},
		{"err:identity:forbidden", fasthttp.StatusForbidden},
		{"err:session:unauthorized", fasthttp.StatusUnauthorized},
		{"err:envelope:conflict", fasthttp.StatusConflict},
		{"err:envelope:alreadyExists", fasthttp.StatusConflict},
		{"err:envelope:expired", fasthttp.StatusGone},
		{"err:document:invalid", fasthttp.StatusBadRequest},
		{"err:document:required", fasthttp.StatusBadRequest},
		// Unknown reason and malformed code both map to a safe 500.
		{"err:document:teapot", fasthttp.StatusInternalServerError},
		{"not-a-code", fasthttp.StatusInternalServerError},
	}

	for _, c := range cases {
		err := pkerrors.FromResultCode(c.code)

		sc, ok := err.(statusCoder)
		qt.Assert(t, qt.IsTrue(ok), qt.Commentf("%q should map to a status-coded error, got %T", c.code, err))
		qt.Check(t, qt.Equals(sc.StatusCode(), c.status), qt.Commentf("status for %q", c.code))
	}
}

func TestFromResultCode_NoInternalLeak(t *testing.T) {
	// An unmapped/internal error must never expose its raw cause to the client.
	err := pkerrors.FromResultCode("err:document:teapot")

	se, ok := err.(safeErrorer)
	qt.Assert(t, qt.IsTrue(ok))
	qt.Check(t, qt.Equals(se.SafeError(), "internal server error"))
}

func TestFromResultCode_NotFoundResource(t *testing.T) {
	err := pkerrors.FromResultCode("err:document:notFound")
	qt.Check(t, qt.Equals(err.Error(), "document not found"))

	// An explicit safe message overrides the resource label.
	err = pkerrors.FromResultCode("err:document:notFound", "the requested document")
	qt.Check(t, qt.Equals(err.Error(), "the requested document not found"))
}

func TestHTTP(t *testing.T) {
	err := pkerrors.HTTP("envelope", "conflict")

	sc, ok := err.(statusCoder)
	qt.Assert(t, qt.IsTrue(ok))
	qt.Check(t, qt.Equals(sc.StatusCode(), fasthttp.StatusConflict))
}

func TestConflictError_Messages(t *testing.T) {
	err := pkerrors.FromResultCode("err:envelope:conflict")
	qt.Check(t, qt.Equals(err.Error(), "envelope conflict"))

	se, ok := err.(safeErrorer)
	qt.Assert(t, qt.IsTrue(ok))
	qt.Check(t, qt.Equals(se.SafeError(), "envelope conflict"))

	// No resource label falls back to a generic message.
	var empty pkerrors.ConflictError
	qt.Check(t, qt.Equals(empty.Error(), "conflict"))
}

func TestGoneError_Messages(t *testing.T) {
	err := pkerrors.FromResultCode("err:envelope:expired")
	qt.Check(t, qt.Equals(err.Error(), "envelope no longer available"))

	se, ok := err.(safeErrorer)
	qt.Assert(t, qt.IsTrue(ok))
	qt.Check(t, qt.Equals(se.SafeError(), "envelope no longer available"))

	// No resource label falls back to a generic message.
	var empty pkerrors.GoneError
	qt.Check(t, qt.Equals(empty.Error(), "no longer available"))
}

func TestInternalError_UnwrapAndError(t *testing.T) {
	cause := stderrors.New("db timeout")
	err := pkerrors.InternalError{Err: cause}

	qt.Check(t, qt.Equals(err.Error(), "db timeout"))
	qt.Check(t, qt.Equals(stderrors.Unwrap(err), cause))

	var empty pkerrors.InternalError
	qt.Check(t, qt.Equals(empty.Error(), "internal error"))
}

func TestFromResultCode_PreservesRawCauseInLogOnly(t *testing.T) {
	// An unparseable code and an unrecognized reason both keep the raw code in
	// the internal (log-side) error text — never in SafeError — so an operator
	// can still find the offending code in the logs.
	qt.Check(t, qt.Equals(pkerrors.FromResultCode("not-a-code").Error(), "not-a-code"))
	qt.Check(t, qt.Equals(pkerrors.FromResultCode("err:document:teapot").Error(), "err:document:teapot"))
}
