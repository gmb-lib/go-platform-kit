package errors_test

import (
	stderrors "errors"
	"iter"
	"testing"

	"azugo.io/azugo"
	"github.com/go-quicktest/qt"
	"github.com/valyala/fasthttp"

	"github.com/gmb-lib/go-platform-kit/correlation"
	pkerrors "github.com/gmb-lib/go-platform-kit/errors"
)

// headerError is a stand-in for a service error that carries response headers
// (e.g. Retry-After) — the azugo.ErrorHeaders path Handler preserves.
type headerError struct{}

func (headerError) Error() string     { return "rate limited" }
func (headerError) SafeError() string { return "slow down" }
func (headerError) StatusCode() int   { return fasthttp.StatusTooManyRequests }
func (headerError) ErrorHeaders() iter.Seq2[string, string] {
	return func(yield func(string, string) bool) {
		yield("Retry-After", "30")
	}
}

func TestHandler_NilErrorFallsThrough(t *testing.T) {
	h := pkerrors.Handler("document-svc", false)
	qt.Check(t, qt.IsFalse(h(nil, nil)))
}

func TestHandler_RendersBareErrorAsUniform500(t *testing.T) {
	app := azugo.NewTestApp()
	app.AppName = "document-svc"
	app.RouterOptions().ErrorHandler = pkerrors.Handler(app.AppName, false)

	app.Get("/boom", func(ctx *azugo.Context) {
		ctx.Error(stderrors.New("boom"))
	})
	app.Start(t)

	defer app.Stop()

	resp, err := app.TestClient().Get("/boom")
	qt.Assert(t, qt.IsNil(err))

	defer fasthttp.ReleaseResponse(resp)

	qt.Check(t, qt.Equals(resp.StatusCode(), fasthttp.StatusInternalServerError))
	qt.Check(t, qt.StringContains(string(resp.Header.ContentType()), pkerrors.ContentTypeProblemJSON))

	body, err := resp.BodyUncompressed()
	qt.Assert(t, qt.IsNil(err))
	qt.Check(t, qt.StringContains(string(body), `"code":"err:internal:unexpected"`))
	qt.Check(t, qt.StringContains(string(body), `"source":"document-svc"`))
}

func TestHandler_PreservesProblemCodeAndTraceID(t *testing.T) {
	app := azugo.NewTestApp()
	app.AppName = "document-svc"
	app.Use(correlation.Middleware())
	app.RouterOptions().ErrorHandler = pkerrors.Handler(app.AppName, false)

	app.Get("/boom", func(ctx *azugo.Context) {
		ctx.Error(pkerrors.NewProblem("err:document:notFound"))
	})
	app.Start(t)

	defer app.Stop()

	tc := app.TestClient()
	resp, err := tc.Get("/boom", tc.WithHeader(correlation.HeaderCorrelationID, "corr-h1"))
	qt.Assert(t, qt.IsNil(err))

	defer fasthttp.ReleaseResponse(resp)

	qt.Check(t, qt.Equals(resp.StatusCode(), fasthttp.StatusNotFound))

	body, err := resp.BodyUncompressed()
	qt.Assert(t, qt.IsNil(err))
	qt.Check(t, qt.StringContains(string(body), `"code":"err:document:notFound"`))
	// traceThread falls back to the correlation id when tracing is off.
	qt.Check(t, qt.StringContains(string(body), `"trace_id":"corr-h1"`))
}

func TestHandler_PublicModeStripsSourceAndChain(t *testing.T) {
	app := azugo.NewTestApp()
	app.AppName = "portal-bff"
	app.RouterOptions().ErrorHandler = pkerrors.Handler(app.AppName, true)

	app.Get("/boom", func(ctx *azugo.Context) {
		ctx.Error(pkerrors.NewProblem("err:document:notFound", pkerrors.WithSource("document-store")))
	})
	app.Start(t)

	defer app.Stop()

	resp, err := app.TestClient().Get("/boom")
	qt.Assert(t, qt.IsNil(err))

	defer fasthttp.ReleaseResponse(resp)

	body, err := resp.BodyUncompressed()
	qt.Assert(t, qt.IsNil(err))
	qt.Check(t, qt.Not(qt.StringContains(string(body), `"source"`)))
}

func TestHandler_PreservesErrorHeaders(t *testing.T) {
	app := azugo.NewTestApp()
	app.RouterOptions().ErrorHandler = pkerrors.Handler("document-svc", false)

	app.Get("/boom", func(ctx *azugo.Context) {
		ctx.Error(headerError{})
	})
	app.Start(t)

	defer app.Stop()

	resp, err := app.TestClient().Get("/boom")
	qt.Assert(t, qt.IsNil(err))

	defer fasthttp.ReleaseResponse(resp)

	qt.Check(t, qt.Equals(resp.StatusCode(), fasthttp.StatusTooManyRequests))
	qt.Check(t, qt.Equals(string(resp.Header.Peek("Retry-After")), "30"))
}
