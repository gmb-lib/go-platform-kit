package errors_test

import (
	"testing"

	"github.com/go-quicktest/qt"
	"github.com/valyala/fasthttp"

	pkerrors "github.com/gmb-sig/go-platform-kit/errors"
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
