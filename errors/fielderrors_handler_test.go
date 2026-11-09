package errors_test

import (
	"testing"

	"azugo.io/azugo"
	"github.com/go-quicktest/qt"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	pkerrors "github.com/gmb-lib/go-platform-kit/errors"
)

const fieldRequired = `"errors":[{"field":15,"reason":"required"}]`

// renderOnce runs one request through a real renderer and returns the status and body.
func renderOnce(t *testing.T, public bool, fail func(ctx *azugo.Context)) (int, string, *observer.ObservedLogs) {
	t.Helper()

	app := azugo.NewTestApp()
	app.AppName = "a-service"
	app.RouterOptions().ErrorHandler = pkerrors.Handler(app.AppName, public, nil)
	app.Get("/boom", fail)
	app.Start(t)

	defer app.Stop()

	obs, logs := observer.New(zap.InfoLevel)
	qt.Assert(t, qt.IsNil(app.ReplaceLogger(zap.New(obs))))

	resp, err := app.TestClient().Get("/boom")
	qt.Assert(t, qt.IsNil(err))

	defer fasthttp.ReleaseResponse(resp)

	body, err := resp.BodyUncompressed()
	qt.Assert(t, qt.IsNil(err))

	return resp.StatusCode(), string(body), logs
}

func TestHandler_BothEnvelopesCarryTheFieldList(t *testing.T) {
	for name, public := range map[string]bool{"internal": false, "public": true} {
		status, body, _ := renderOnce(t, public, func(ctx *azugo.Context) {
			ctx.Error(pkerrors.NewProblem("err:form:invalid",
				pkerrors.WithFieldErrors(pkerrors.FieldError{Field: 15, Reason: "required"}),
			))
		})

		qt.Check(t, qt.Equals(status, fasthttp.StatusBadRequest), qt.Commentf(name))
		qt.Check(t, qt.StringContains(body, fieldRequired), qt.Commentf("%s: %s", name, body))
	}
}

func TestHandler_PublicEnvelopeKeepsTheListAndStillStripsTopology(t *testing.T) {
	_, body, _ := renderOnce(t, true, func(ctx *azugo.Context) {
		ctx.Error(pkerrors.NewProblem("err:form:invalid",
			pkerrors.WithSource("a-downstream"),
			pkerrors.WithDetail("an internal sentence"),
			pkerrors.WithFieldErrors(pkerrors.FieldError{Field: 15, Reason: "required"}),
		))
	})

	qt.Check(t, qt.StringContains(body, fieldRequired))
	qt.Check(t, qt.Not(qt.StringContains(body, `"source"`)))
	qt.Check(t, qt.Not(qt.StringContains(body, `"chain"`)))
	qt.Check(t, qt.Not(qt.StringContains(body, "an internal sentence")))
}

// TestHandler_ARelayedListReachesThePublicBody is the whole trip: a downstream's body is
// decoded, relayed by this service, and projected at the public edge.
func TestHandler_ARelayedListReachesThePublicBody(t *testing.T) {
	downstream := `{"title":"A required field has no value","status":400,"code":"err:form:fieldRequired",` +
		`"detail":"an internal sentence","source":"a-downstream",` + fieldRequired + `}`

	status, body, _ := renderOnce(t, true, func(ctx *azugo.Context) {
		down, ok := pkerrors.ParseProblem([]byte(downstream))
		qt.Assert(t, qt.IsTrue(ok))
		ctx.Error(pkerrors.Relay(down, "a-service", down.Status))
	})

	qt.Check(t, qt.Equals(status, fasthttp.StatusBadRequest))
	qt.Check(t, qt.StringContains(body, fieldRequired), qt.Commentf(body))
	qt.Check(t, qt.StringContains(body, `"code":"err:form:fieldRequired"`))
	qt.Check(t, qt.Not(qt.StringContains(body, "an internal sentence")))
	qt.Check(t, qt.Not(qt.StringContains(body, `"source"`)))
}

// TestHandler_ABadRelayedListIsDroppedWholeAndLoggedOnce: the error still reaches the
// caller, without the list, and the one log line for the error says the list was refused.
func TestHandler_ABadRelayedListIsDroppedWholeAndLoggedOnce(t *testing.T) {
	downstream := `{"title":"A required field has no value","status":400,"code":"err:form:fieldRequired",` +
		`"errors":[{"field":15,"reason":"required"},{"field":16,"reason":"a sentence, not a word"}]}`

	for name, public := range map[string]bool{"internal": false, "public": true} {
		status, body, logs := renderOnce(t, public, func(ctx *azugo.Context) {
			down, ok := pkerrors.ParseProblem([]byte(downstream))
			qt.Assert(t, qt.IsTrue(ok))
			ctx.Error(pkerrors.Relay(down, "a-service", down.Status))
		})

		qt.Check(t, qt.Equals(status, fasthttp.StatusBadRequest), qt.Commentf(name))
		qt.Check(t, qt.Not(qt.StringContains(body, `"errors"`)), qt.Commentf("%s: %s", name, body))
		qt.Check(t, qt.StringContains(body, `"code":"err:form:fieldRequired"`), qt.Commentf(name))

		lines := logs.FilterMessage("request error").All()
		qt.Assert(t, qt.HasLen(lines, 1), qt.Commentf("%s: one line per rendered error", name))
		qt.Check(t, qt.Equals(lines[0].ContextMap()["error.field_errors_dropped"], any(true)), qt.Commentf(name))
	}
}

func TestHandler_AListSetByHandThatIsNotValidIsDroppedAndLogged(t *testing.T) {
	for name, public := range map[string]bool{"internal": false, "public": true} {
		_, body, logs := renderOnce(t, public, func(ctx *azugo.Context) {
			p := pkerrors.NewProblem("err:form:invalid")
			p.Errors = pkerrors.FieldErrors{{Field: 15, Reason: "required"}, {Field: 16, Reason: "Not A Word"}}
			ctx.Error(p)
		})

		// Nothing of the list is written; the internal body may hold an empty "errors":null.
		qt.Check(t, qt.Not(qt.StringContains(body, `"field"`)), qt.Commentf("%s: %s", name, body))
		qt.Check(t, qt.Not(qt.StringContains(body, "Not A Word")), qt.Commentf("%s: %s", name, body))
		qt.Check(t, qt.StringContains(body, `"code":"err:form:invalid"`), qt.Commentf(name))

		lines := logs.FilterMessage("request error").All()
		qt.Assert(t, qt.HasLen(lines, 1), qt.Commentf(name))
		qt.Check(t, qt.Equals(lines[0].ContextMap()["error.field_errors_dropped"], any(true)), qt.Commentf(name))
	}
}

// TestHandler_TheFrameworksOwnEncoderChecksTheListToo: a service that skips the renderer and
// hands a Problem to ctx.JSON is encoded by the web framework's own encoder, which is not
// encoding/json; the list's type is what holds there.
func TestHandler_TheFrameworksOwnEncoderChecksTheListToo(t *testing.T) {
	app := azugo.NewTestApp()
	app.AppName = "a-service"
	app.Get("/p", func(ctx *azugo.Context) {
		p := pkerrors.NewProblem("err:form:invalid")
		p.Errors = pkerrors.FieldErrors{{Field: 1, Reason: "a sentence leaks"}}
		ctx.StatusCode(fasthttp.StatusBadRequest)
		ctx.JSON(p)
	})
	app.Get("/ok", func(ctx *azugo.Context) {
		p := pkerrors.NewProblem("err:form:invalid", pkerrors.WithFieldErrors(pkerrors.FieldError{Field: 15, Reason: "required"}))
		ctx.StatusCode(fasthttp.StatusBadRequest)
		ctx.JSON(p)
	})
	app.Start(t)

	defer app.Stop()

	read := func(path string) string {
		resp, err := app.TestClient().Get(path)
		qt.Assert(t, qt.IsNil(err))

		defer fasthttp.ReleaseResponse(resp)

		body, err := resp.BodyUncompressed()
		qt.Assert(t, qt.IsNil(err))

		return string(body)
	}

	bad := read("/p")
	qt.Check(t, qt.Not(qt.StringContains(bad, "a sentence leaks")), qt.Commentf(bad))
	qt.Check(t, qt.Not(qt.StringContains(bad, `"field"`)), qt.Commentf(bad))

	qt.Check(t, qt.StringContains(read("/ok"), fieldRequired))
}

func TestHandler_AValidListLeavesTheLogLineAsItWas(t *testing.T) {
	_, _, logs := renderOnce(t, false, func(ctx *azugo.Context) {
		ctx.Error(pkerrors.NewProblem("err:form:invalid",
			pkerrors.WithFieldErrors(pkerrors.FieldError{Field: 15, Reason: "required"}),
		))
	})

	lines := logs.FilterMessage("request error").All()
	qt.Assert(t, qt.HasLen(lines, 1))

	_, noted := lines[0].ContextMap()["error.field_errors_dropped"]
	qt.Check(t, qt.IsFalse(noted))
}
