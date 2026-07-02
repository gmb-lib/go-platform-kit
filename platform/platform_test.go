package platform_test

import (
	"context"
	"testing"

	"azugo.io/azugo"
	"azugo.io/opentelemetry"
	"github.com/go-quicktest/qt"
	"github.com/valyala/fasthttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/gmb-lib/go-platform-kit/config"
	"github.com/gmb-lib/go-platform-kit/correlation"
	pkerrors "github.com/gmb-lib/go-platform-kit/errors"
	"github.com/gmb-lib/go-platform-kit/platform"
)

func TestSetup_NilAppErrors(t *testing.T) {
	err := platform.Setup(nil, platform.Options{Config: config.New()})

	qt.Assert(t, qt.IsNotNil(err))
}

func TestSetup_NilConfigErrors(t *testing.T) {
	app := azugo.New()
	app.AppName = "document-svc"

	err := platform.Setup(app, platform.Options{})

	qt.Assert(t, qt.IsNotNil(err))
}

func TestSetup_WiresErrorHandlerAndCorrelation(t *testing.T) {
	app := azugo.NewTestApp()
	app.AppName = "document-svc"

	qt.Assert(t, qt.IsNil(platform.Setup(app.App, platform.Options{Config: config.New()})))

	app.Get("/boom", func(ctx *azugo.Context) {
		ctx.Error(pkerrors.NewProblem("err:document:notFound"))
	})
	app.Start(t)

	defer app.Stop()

	tc := app.TestClient()
	resp, err := tc.Get("/boom", tc.WithHeader(correlation.HeaderCorrelationID, "corr-plat"))
	qt.Assert(t, qt.IsNil(err))

	defer fasthttp.ReleaseResponse(resp)

	// The uniform RFC 9457 error handler replaced the framework default.
	qt.Check(t, qt.Equals(resp.StatusCode(), fasthttp.StatusNotFound))
	qt.Check(t, qt.StringContains(string(resp.Header.ContentType()), pkerrors.ContentTypeProblemJSON))

	body, err := resp.BodyUncompressed()
	qt.Assert(t, qt.IsNil(err))
	qt.Check(t, qt.StringContains(string(body), `"code":"err:document:notFound"`))

	// The correlation middleware (installed by Setup) echoes the bound id.
	qt.Check(t, qt.Equals(string(resp.Header.Peek(correlation.HeaderCorrelationID)), "corr-plat"))
}

func TestSetup_PublicErrorsStripsSourceAndChain(t *testing.T) {
	app := azugo.NewTestApp()
	app.AppName = "portal-bff"

	qt.Assert(t, qt.IsNil(platform.Setup(app.App, platform.Options{Config: config.New(), PublicErrors: true})))

	app.Get("/boom", func(ctx *azugo.Context) {
		ctx.Error(pkerrors.NewProblem("err:document:notFound"))
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

// noopRecorder is a minimal InstrumentationRecorderFunc stand-in for a
// service's custom instrumentation (e.g. DB query tracing).
func noopRecorder(_ context.Context, _ trace.Tracer, _ propagation.TextMapPropagator, _ opentelemetry.InstrumentationSpanNameFormatter, _ string, _ ...any) (func(err error), bool) {
	return nil, false
}

func TestSetup_ForwardsTracingOptions(t *testing.T) {
	app := azugo.NewTestApp()
	app.AppName = "document-svc"

	// A service with a custom instrumentation recorder (e.g. jsondb DB query
	// tracing) must be able to keep it while adopting Setup.
	err := platform.Setup(app.App, platform.Options{
		Config: config.New(),
		TracingOptions: []opentelemetry.Option{
			opentelemetry.InstrumentationRecorder("db", noopRecorder),
		},
	})
	qt.Assert(t, qt.IsNil(err))
}
