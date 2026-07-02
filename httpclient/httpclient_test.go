package httpclient_test

import (
	"testing"

	"azugo.io/azugo"
	"azugo.io/core/http"
	"github.com/go-quicktest/qt"
	"github.com/valyala/fasthttp"

	"github.com/gmb-lib/go-platform-kit/correlation"
	"github.com/gmb-lib/go-platform-kit/httpclient"
)

// withCtx runs fn inside a real request handler so it receives a fully
// initialized *azugo.Context. fn must only capture into outer variables;
// assert in the test goroutine after withCtx returns.
func withCtx(t *testing.T, fn func(ctx *azugo.Context)) {
	t.Helper()

	app := azugo.NewTestApp()
	app.Get("/t", func(ctx *azugo.Context) {
		fn(ctx)
		ctx.StatusCode(fasthttp.StatusNoContent)
	})
	app.Start(t)

	defer app.Stop()

	resp, err := app.TestClient().Get("/t")
	qt.Assert(t, qt.IsNil(err))
	fasthttp.ReleaseResponse(resp)
}

func TestCorrelationOptions_NoBoundIDReturnsEmpty(t *testing.T) {
	var opts []http.RequestOption

	withCtx(t, func(ctx *azugo.Context) {
		opts = httpclient.CorrelationOptions(ctx)
	})

	qt.Check(t, qt.HasLen(opts, 0))
}

func TestCorrelationOptions_PropagatesBoundID(t *testing.T) {
	var opts []http.RequestOption

	app := azugo.NewTestApp()
	app.Use(correlation.Middleware())
	app.Get("/t", func(ctx *azugo.Context) {
		opts = httpclient.CorrelationOptions(ctx)
		ctx.StatusCode(fasthttp.StatusNoContent)
	})
	app.Start(t)

	defer app.Stop()

	tc := app.TestClient()
	resp, err := tc.Get("/t", tc.WithHeader(correlation.HeaderCorrelationID, "corr-1"))
	qt.Assert(t, qt.IsNil(err))
	fasthttp.ReleaseResponse(resp)

	qt.Check(t, qt.HasLen(opts, 1))
}

func TestOutbound_SetsBaseURL(t *testing.T) {
	var baseURL string

	withCtx(t, func(ctx *azugo.Context) {
		baseURL = httpclient.Outbound(ctx, "https://document-svc").BaseURL()
	})

	qt.Check(t, qt.Equals(baseURL, "https://document-svc"))
}
