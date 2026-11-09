package errors_test

import (
	"fmt"
	"sync"
	"testing"

	"azugo.io/azugo"
	"github.com/go-quicktest/qt"
	"github.com/valyala/fasthttp"

	"github.com/gmb-lib/go-platform-kit/correlation"
	pkerrors "github.com/gmb-lib/go-platform-kit/errors"
)

// TestHandler_ASharedProblemIsNotWrittenToAndKeepsNoRequestsTraceID: a Problem kept in a
// variable and returned for many requests is rendered from a copy. Before, the renderer filled
// its source and trace id into the very value it was handed, so the first request's trace id
// came back on every later response and concurrent requests raced on the write.
func TestHandler_ASharedProblemIsNotWrittenToAndKeepsNoRequestsTraceID(t *testing.T) {
	shared := pkerrors.NewProblem("err:document:notFound")

	app := azugo.NewTestApp()
	app.AppName = "document-svc"
	app.Use(correlation.Middleware())
	app.RouterOptions().ErrorHandler = pkerrors.Handler(app.AppName, false, nil)
	app.Get("/boom", func(ctx *azugo.Context) { ctx.Error(shared) })
	app.Start(t)

	defer app.Stop()

	get := func(corr string) string {
		tc := app.TestClient()
		resp, err := tc.Get("/boom", tc.WithHeader(correlation.HeaderCorrelationID, corr))
		qt.Assert(t, qt.IsNil(err))

		defer fasthttp.ReleaseResponse(resp)

		body, err := resp.BodyUncompressed()
		qt.Assert(t, qt.IsNil(err))

		return string(body)
	}

	first := get("corr-one")
	second := get("corr-two")

	qt.Check(t, qt.StringContains(first, `"trace_id":"corr-one"`), qt.Commentf(first))
	qt.Check(t, qt.StringContains(second, `"trace_id":"corr-two"`), qt.Commentf("a later request got another's trace id: %s", second))
	qt.Check(t, qt.Equals(shared.TraceID, ""))
	qt.Check(t, qt.Equals(shared.Source, ""))

	// Many at once: under the race detector a write into the shared value fails here.
	var wg sync.WaitGroup

	for i := 0; i < 6; i++ {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			for j := 0; j < 5; j++ {
				corr := fmt.Sprintf("corr-%d-%d", i, j)

				body := get(corr)
				qt.Check(t, qt.StringContains(body, `"trace_id":"`+corr+`"`), qt.Commentf(body))
			}
		}(i)
	}

	wg.Wait()

	qt.Check(t, qt.Equals(shared.TraceID, ""))
	qt.Check(t, qt.Equals(shared.Source, ""))
}
