package observability

import (
	"context"
	"strings"
	"testing"
	"time"

	"azugo.io/azugo"
	"azugo.io/core/ratelimit"
	"azugo.io/opentelemetry"
	"github.com/go-quicktest/qt"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// A rate limiter's key: here a client's address, as the framework's default key is.
const (
	clientAddress = "203.0.113.7"
	limiterKey    = "ip:" + clientAddress
)

// limit reports one rate-limit operation the way the framework's limiter does.
func limit(app *azugo.App, op string) {
	var args []any
	switch op {
	case ratelimit.InstrumentationAllow, ratelimit.InstrumentationPeek:
		args = []any{limiterKey, &ratelimit.Result{Allowed: false, Remaining: 0, RetryAfter: 2 * time.Second}}
	default:
		args = []any{limiterKey}
	}
	app.Instrumenter().Observe(context.Background(), op, args...)(nil)
}

// Each rate-limit operation records a span named by the operation alone, with the
// limiter's answer, and the key in none of its texts.
func TestNoRateLimitSpanCarriesTheKey(t *testing.T) {
	app, exporter := tracedApp(t)

	for _, op := range []string{ratelimit.InstrumentationAllow, ratelimit.InstrumentationPeek,
		ratelimit.InstrumentationWait, ratelimit.InstrumentationReset} {
		limit(app, op)
	}

	spans := exporter.GetSpans()
	qt.Assert(t, qt.HasLen(spans, 4))
	for i, want := range []string{"ALLOW", "PEEK", "WAIT", "RESET"} {
		s := spans[i]
		qt.Check(t, qt.Equals(s.Name, want))
		qt.Check(t, qt.Equals(attrs(s)["service.peer.name"], "ratelimit"))
		for _, text := range spanTexts(s) {
			qt.Check(t, qt.IsFalse(strings.Contains(text, clientAddress)),
				qt.Commentf("span %q carries the key in %q", s.Name, text))
		}
	}
	allow := attrs(spans[0])
	qt.Check(t, qt.Equals(allow["ratelimit.allowed"], "false"))
	qt.Check(t, qt.Equals(allow["ratelimit.remaining"], "0"))
	qt.Check(t, qt.Equals(allow["ratelimit.retry_after_ms"], "2000"))
}

// A service's own span name formatter still names the rate-limit spans, and a
// service's own recorder for them is not called.
func TestRateLimitSpansKeepAServicesFormatterAndSkipItsRecorder(t *testing.T) {
	calls := 0
	own := func(context.Context, trace.Tracer, propagation.TextMapPropagator, opentelemetry.InstrumentationSpanNameFormatter, string, ...any) (func(error), bool) {
		calls++

		return func(error) {}, true
	}
	format := opentelemetry.InstrumentationSpanNameFormatter(func(_ context.Context, op string, _ ...any) string {
		if op == ratelimit.InstrumentationAllow {
			return "sign-in limit"
		}

		return ""
	})
	app, exporter := tracedApp(t, format, opentelemetry.InstrumentationRecorder("own-limit", own, ratelimit.InstrumentationAllow))

	limit(app, ratelimit.InstrumentationAllow)

	spans := exporter.GetSpans()
	qt.Assert(t, qt.HasLen(spans, 1))
	qt.Check(t, qt.Equals(spans[0].Name, "sign-in limit"))
	qt.Check(t, qt.Equals(calls, 0))
}
