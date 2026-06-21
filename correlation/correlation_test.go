package correlation_test

import (
	"encoding/json"
	"testing"

	"azugo.io/azugo"
	"github.com/go-quicktest/qt"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/gmb-sig/go-platform-kit/broker"
	"github.com/gmb-sig/go-platform-kit/correlation"
	"github.com/gmb-sig/go-platform-kit/httpclient"
)

// TestPropagation_EndToEnd covers inbound id → ctx → outbound header option →
// broker envelope, plus the response echoing the correlation header.
func TestPropagation_EndToEnd(t *testing.T) {
	app := azugo.NewTestApp()
	app.Use(correlation.Middleware())

	app.Get("/probe", func(ctx *azugo.Context) {
		ids := correlation.FromContext(ctx)

		ev := &broker.Envelope{
			EventType:  "document.previewed",
			Categories: []broker.Category{broker.CategorySigning},
			Outcome:    broker.OutcomeSuccess,
		}
		broker.Stamp(ctx, ev)

		ctx.JSON(map[string]any{
			"id":            ids.CorrelationID,
			"envelope_corr": ev.CorrelationID,
			"opt_present":   len(httpclient.CorrelationOptions(ctx)) == 1,
		})
	})

	app.Start(t)

	defer app.Stop()

	tc := app.TestClient()
	resp, err := tc.Get("/probe", tc.WithHeader(correlation.HeaderCorrelationID, "corr-xyz"))
	qt.Assert(t, qt.IsNil(err))

	defer fasthttp.ReleaseResponse(resp)

	qt.Assert(t, qt.Equals(resp.StatusCode(), fasthttp.StatusOK))
	// Response echoes the correlation id.
	qt.Check(t, qt.Equals(string(resp.Header.Peek(correlation.HeaderCorrelationID)), "corr-xyz"))

	body, err := resp.BodyUncompressed()
	qt.Assert(t, qt.IsNil(err))

	m := map[string]any{}
	qt.Assert(t, qt.IsNil(json.Unmarshal(body, &m)))

	qt.Check(t, qt.Equals(str(m["id"]), "corr-xyz"))
	qt.Check(t, qt.Equals(str(m["envelope_corr"]), "corr-xyz"))

	present, _ := m["opt_present"].(bool)
	qt.Check(t, qt.IsTrue(present))
}

// TestPropagation_LogFields covers "inbound id → log fields": the correlation id
// appears on the request logger for handler log lines.
func TestPropagation_LogFields(t *testing.T) {
	app := azugo.NewTestApp()
	app.Use(correlation.Middleware())
	app.Get("/log", func(ctx *azugo.Context) {
		ctx.Log().Info("probe")
		ctx.StatusCode(fasthttp.StatusNoContent)
	})
	app.Start(t)

	defer app.Stop()

	// Install an observable logger after Start (Start's initLogs would otherwise
	// own the logger); per-request loggers derive from app.Log() at acquire time.
	obs, logs := observer.New(zap.InfoLevel)
	qt.Assert(t, qt.IsNil(app.ReplaceLogger(zap.New(obs))))

	tc := app.TestClient()
	resp, err := tc.Get("/log", tc.WithHeader(correlation.HeaderCorrelationID, "corr-log"))
	qt.Assert(t, qt.IsNil(err))
	fasthttp.ReleaseResponse(resp)

	entry, ok := findEntry(logs, "probe")
	qt.Assert(t, qt.IsTrue(ok), qt.Commentf("expected a 'probe' log entry"))
	qt.Check(t, qt.Equals(str(entry.ContextMap()[correlation.LogKeyCorrelationID]), "corr-log"))
}

// TestMiddleware_GeneratesIDWhenAbsent: with no inbound header the correlation
// id adopts Azugo's own per-request id (ctx.ID(), a 26-char ULID) and echoes it.
func TestMiddleware_GeneratesIDWhenAbsent(t *testing.T) {
	app := azugo.NewTestApp()
	app.Use(correlation.Middleware())
	app.Get("/cid", func(ctx *azugo.Context) {
		ctx.Text(correlation.ID(ctx))
	})
	app.Start(t)

	defer app.Stop()

	tc := app.TestClient()
	resp, err := tc.Get("/cid")
	qt.Assert(t, qt.IsNil(err))

	defer fasthttp.ReleaseResponse(resp)

	body, err := resp.BodyUncompressed()
	qt.Assert(t, qt.IsNil(err))

	qt.Check(t, qt.Equals(len(string(body)), 26)) // ULID length
	// The generated id is echoed in the response header.
	qt.Check(t, qt.Equals(string(resp.Header.Peek(correlation.HeaderCorrelationID)), string(body)))
}

func findEntry(logs *observer.ObservedLogs, msg string) (observer.LoggedEntry, bool) {
	for _, e := range logs.All() {
		if e.Message == msg {
			return e, true
		}
	}

	return observer.LoggedEntry{}, false
}

// str extracts a string from a decoded value.
func str(v any) string {
	s, _ := v.(string)

	return s
}
