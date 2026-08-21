package httpclient_test

import (
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"azugo.io/azugo"
	"azugo.io/core/http"
	"github.com/go-quicktest/qt"
	"github.com/valyala/fasthttp"

	"github.com/gmb-lib/go-platform-kit/correlation"
	"github.com/gmb-lib/go-platform-kit/httpclient"
	"github.com/gmb-lib/go-platform-kit/propagation"
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

// gotHeaders records what an upstream service actually received, so the
// outbound tests assert the headers on the wire rather than the number of
// options returned — a count passes just as happily when both options carry
// the same header.
type gotHeaders struct {
	correlationID string
	appInstanceID string
}

// upstream stands in for the service being called: a real HTTP server the
// outbound client can reach by URL, capturing the headers of the one request
// it serves.
func upstream(t *testing.T, got *gotHeaders) string {
	t.Helper()

	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		got.correlationID = r.Header.Get(correlation.HeaderCorrelationID)
		got.appInstanceID = r.Header.Get(propagation.HeaderAppInstanceID)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	return srv.URL
}

func TestCorrelationOptions_NoBoundIDReturnsEmpty(t *testing.T) {
	var opts []http.RequestOption

	withCtx(t, func(ctx *azugo.Context) {
		opts = httpclient.CorrelationOptions(ctx)
	})

	qt.Check(t, qt.HasLen(opts, 0))
}

// TestCorrelationOptions_ForwardsBothHeaders proves the end the review cares
// about: with both ids bound inbound, an outbound call made through Outbound
// with the options spread arrives upstream carrying both headers.
func TestCorrelationOptions_ForwardsBothHeaders(t *testing.T) {
	got := &gotHeaders{}
	target := upstream(t, got)

	var callErr error

	app := azugo.NewTestApp()
	app.Use(correlation.Middleware())
	app.Get("/t", func(ctx *azugo.Context) {
		out := map[string]any{}
		callErr = httpclient.Outbound(ctx, target).GetJSON("/", &out, httpclient.CorrelationOptions(ctx)...)
		ctx.StatusCode(fasthttp.StatusNoContent)
	})
	app.Start(t)

	defer app.Stop()

	tc := app.TestClient()
	resp, err := tc.Get("/t",
		tc.WithHeader(correlation.HeaderCorrelationID, "corr-1"),
		tc.WithHeader(correlation.HeaderAppInstanceID, "instance-xyz"))
	qt.Assert(t, qt.IsNil(err))
	fasthttp.ReleaseResponse(resp)

	qt.Assert(t, qt.IsNil(callErr))
	qt.Check(t, qt.Equals(got.correlationID, "corr-1"))
	qt.Check(t, qt.Equals(got.appInstanceID, "instance-xyz"))
}

// TestCorrelationOptions_NoAppInstanceHeaderForwardsOnlyCorrelation covers the
// absent-header path: with no inbound instance id there is nothing to forward,
// and the correlation id must still arrive.
func TestCorrelationOptions_NoAppInstanceHeaderForwardsOnlyCorrelation(t *testing.T) {
	got := &gotHeaders{}
	target := upstream(t, got)

	var (
		callErr  error
		optCount int
	)

	app := azugo.NewTestApp()
	app.Use(correlation.Middleware())
	app.Get("/t", func(ctx *azugo.Context) {
		opts := httpclient.CorrelationOptions(ctx)
		optCount = len(opts)

		out := map[string]any{}
		callErr = httpclient.Outbound(ctx, target).GetJSON("/", &out, opts...)
		ctx.StatusCode(fasthttp.StatusNoContent)
	})
	app.Start(t)

	defer app.Stop()

	tc := app.TestClient()
	resp, err := tc.Get("/t", tc.WithHeader(correlation.HeaderCorrelationID, "corr-1"))
	qt.Assert(t, qt.IsNil(err))
	fasthttp.ReleaseResponse(resp)

	qt.Assert(t, qt.IsNil(callErr))
	qt.Check(t, qt.Equals(optCount, 1))
	qt.Check(t, qt.Equals(got.correlationID, "corr-1"))
	qt.Check(t, qt.Equals(got.appInstanceID, ""))
}

// TestSetCorrelationHeaders_SetsBoth covers the raw-header path used by a client
// that builds its own request instead of passing options — the two paths must
// forward the same two headers.
func TestSetCorrelationHeaders_SetsBoth(t *testing.T) {
	var cid, iid string

	app := azugo.NewTestApp()
	app.Use(correlation.Middleware())
	app.Get("/t", func(ctx *azugo.Context) {
		h := &fasthttp.RequestHeader{}
		httpclient.SetCorrelationHeaders(ctx, h)

		cid = string(h.Peek(correlation.HeaderCorrelationID))
		iid = string(h.Peek(propagation.HeaderAppInstanceID))

		ctx.StatusCode(fasthttp.StatusNoContent)
	})
	app.Start(t)

	defer app.Stop()

	tc := app.TestClient()
	resp, err := tc.Get("/t",
		tc.WithHeader(correlation.HeaderCorrelationID, "corr-1"),
		tc.WithHeader(correlation.HeaderAppInstanceID, "instance-xyz"))
	qt.Assert(t, qt.IsNil(err))
	fasthttp.ReleaseResponse(resp)

	qt.Check(t, qt.Equals(cid, "corr-1"))
	qt.Check(t, qt.Equals(iid, "instance-xyz"))
}

// TestSetCorrelationHeaders_LeavesAbsentIDsUnset pins that an absent instance id
// sets no header at all rather than an empty one — an empty header downstream
// would audit as a present-but-blank value.
func TestSetCorrelationHeaders_LeavesAbsentIDsUnset(t *testing.T) {
	var present bool

	app := azugo.NewTestApp()
	app.Use(correlation.Middleware())
	app.Get("/t", func(ctx *azugo.Context) {
		h := &fasthttp.RequestHeader{}
		httpclient.SetCorrelationHeaders(ctx, h)

		present = h.Peek(propagation.HeaderAppInstanceID) != nil
		ctx.StatusCode(fasthttp.StatusNoContent)
	})
	app.Start(t)

	defer app.Stop()

	resp, err := app.TestClient().Get("/t")
	qt.Assert(t, qt.IsNil(err))
	fasthttp.ReleaseResponse(resp)

	qt.Check(t, qt.IsFalse(present))
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
