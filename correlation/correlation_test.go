package correlation_test

import (
	"encoding/json"
	"strings"
	"testing"

	"azugo.io/azugo"
	"github.com/go-quicktest/qt"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/gmb-lib/go-platform-kit/broker"
	"github.com/gmb-lib/go-platform-kit/correlation"
	"github.com/gmb-lib/go-platform-kit/httpclient"
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

// TestPropagation_AppInstanceID covers "inbound header → ctx → log fields →
// outbound header option", the same treatment as the correlation id, so an app
// instance id is searchable in logs (not just buried in a specific outbound
// call's header attributes). The headers an outbound call actually carries are
// asserted in the httpclient package, where an upstream test server can see
// them; the option count here only pins that a second option appears.
func TestPropagation_AppInstanceID(t *testing.T) {
	app := azugo.NewTestApp()
	app.Use(correlation.Middleware())
	app.Get("/probe", func(ctx *azugo.Context) {
		ctx.Log().Info("probe")
		ctx.JSON(map[string]any{
			"id":        correlation.AppInstanceID(ctx),
			"opt_count": len(httpclient.CorrelationOptions(ctx)),
		})
	})
	app.Start(t)

	defer app.Stop()

	obs, logs := observer.New(zap.InfoLevel)
	qt.Assert(t, qt.IsNil(app.ReplaceLogger(zap.New(obs))))

	tc := app.TestClient()
	resp, err := tc.Get("/probe", tc.WithHeader(correlation.HeaderAppInstanceID, "instance-xyz"))
	qt.Assert(t, qt.IsNil(err))

	defer fasthttp.ReleaseResponse(resp)

	body, err := resp.BodyUncompressed()
	qt.Assert(t, qt.IsNil(err))

	m := map[string]any{}
	qt.Assert(t, qt.IsNil(json.Unmarshal(body, &m)))
	qt.Check(t, qt.Equals(str(m["id"]), "instance-xyz"))

	count, _ := m["opt_count"].(float64)
	qt.Check(t, qt.Equals(int(count), 2))

	entry, ok := findEntry(logs, "probe")
	qt.Assert(t, qt.IsTrue(ok), qt.Commentf("expected a 'probe' log entry"))
	qt.Check(t, qt.Equals(str(entry.ContextMap()[correlation.LogKeyAppInstanceID]), "instance-xyz"))
}

// TestPropagation_NoAppInstanceHeader is the absent-header half: the id is never
// minted, so with no inbound header there must be no bound value, no
// app_instance_id log field, and only the correlation option outbound. Without
// this case, code that defaulted the id to something (the request id, a ULID)
// would pass every other test in this file.
func TestPropagation_NoAppInstanceHeader(t *testing.T) {
	app := azugo.NewTestApp()
	app.Use(correlation.Middleware())
	app.Get("/probe", func(ctx *azugo.Context) {
		ctx.Log().Info("probe")
		ctx.JSON(map[string]any{
			"id":        correlation.AppInstanceID(ctx),
			"opt_count": len(httpclient.CorrelationOptions(ctx)),
		})
	})
	app.Start(t)

	defer app.Stop()

	obs, logs := observer.New(zap.InfoLevel)
	qt.Assert(t, qt.IsNil(app.ReplaceLogger(zap.New(obs))))

	resp, err := app.TestClient().Get("/probe")
	qt.Assert(t, qt.IsNil(err))

	defer fasthttp.ReleaseResponse(resp)

	body, err := resp.BodyUncompressed()
	qt.Assert(t, qt.IsNil(err))

	m := map[string]any{}
	qt.Assert(t, qt.IsNil(json.Unmarshal(body, &m)))
	qt.Check(t, qt.Equals(str(m["id"]), ""))

	count, _ := m["opt_count"].(float64)
	qt.Check(t, qt.Equals(int(count), 1))

	entry, ok := findEntry(logs, "probe")
	qt.Assert(t, qt.IsTrue(ok), qt.Commentf("expected a 'probe' log entry"))

	_, hasField := entry.ContextMap()[correlation.LogKeyAppInstanceID]
	qt.Check(t, qt.IsFalse(hasField))
}

// TestPropagation_OversizedAppInstanceIDIsDropped is the other half of what
// ValidID bounds: a value of entirely legal characters but over the length limit
// must be dropped like any other invalid one. This is the case that would
// otherwise reach a bounded audit column and fail an insert from inside the
// error path.
func TestPropagation_OversizedAppInstanceIDIsDropped(t *testing.T) {
	app := azugo.NewTestApp()
	app.Use(correlation.Middleware())
	app.Get("/probe", func(ctx *azugo.Context) {
		ctx.JSON(map[string]any{"id": correlation.AppInstanceID(ctx)})
	})
	app.Start(t)

	defer app.Stop()

	oversized := strings.Repeat("a", correlation.MaxIDLength+1)

	tc := app.TestClient()
	resp, err := tc.Get("/probe", tc.WithHeader(correlation.HeaderAppInstanceID, oversized))
	qt.Assert(t, qt.IsNil(err))

	defer fasthttp.ReleaseResponse(resp)

	body, err := resp.BodyUncompressed()
	qt.Assert(t, qt.IsNil(err))

	m := map[string]any{}
	qt.Assert(t, qt.IsNil(json.Unmarshal(body, &m)))
	qt.Check(t, qt.Equals(str(m["id"]), ""))
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
