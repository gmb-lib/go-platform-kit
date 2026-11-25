package observability

import (
	"context"
	"errors"
	"strings"
	"testing"

	"azugo.io/azugo"
	"azugo.io/core/cache"
	"azugo.io/opentelemetry"
	"github.com/go-quicktest/qt"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// The key a Redis-backed cache instance named "sessions" reports to the
// instrumenter: its key prefix, its instance and the caller's key.
const (
	secretPart = "the-session-id"
	sessionKey = "svc:sessions:" + secretPart
)

// tracedApp is an app whose tracing EnableTracing switched on, its spans kept in
// memory. The endpoint is never reached: an endpoint is what turns tracing on.
func tracedApp(t *testing.T, opts ...opentelemetry.Option) (*azugo.App, *tracetest.InMemoryExporter) {
	t.Helper()

	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	app := azugo.New()
	app.AppName = "cache-svc"
	opts = append(opts, opentelemetry.TracerProvider(provider))
	qt.Assert(t, qt.IsNil(EnableTracing(app, &opentelemetry.Configuration{Endpoint: "http://127.0.0.1:1"}, opts...)))
	qt.Assert(t, qt.IsFalse(app.Instrumenter().Empty()))

	return app, exporter
}

// observe reports one cache operation the way a Redis-backed instance does.
func observe(app *azugo.App, op string, backend cache.Type, err error) {
	app.Instrumenter().Observe(context.Background(), op, sessionKey, string(backend), "sessions")(err)
}

// spanTexts is every text a span carries: its name, its attributes' keys and
// values, its events and its status.
func spanTexts(s tracetest.SpanStub) []string {
	texts := []string{s.Name, s.Status.Description}
	for _, a := range s.Attributes {
		texts = append(texts, string(a.Key), a.Value.String())
	}
	for _, ev := range s.Events {
		texts = append(texts, ev.Name)
		for _, a := range ev.Attributes {
			texts = append(texts, string(a.Key), a.Value.String())
		}
	}

	return texts
}

func attrs(s tracetest.SpanStub) map[attribute.Key]string {
	m := map[attribute.Key]string{}
	for _, a := range s.Attributes {
		m[a.Key] = a.Value.String()
	}

	return m
}

// A cache read, write and delete through a Redis-backed instance each record a
// span named by the operation and the instance, with the backend, the instance
// and the peer, and the key in none of its texts.
func TestNoCacheSpanCarriesTheKey(t *testing.T) {
	app, exporter := tracedApp(t)

	observe(app, cache.InstrumentationGet, cache.RedisCache, nil)
	observe(app, cache.InstrumentationSet, cache.RedisCache, nil)
	observe(app, cache.InstrumentationDelete, cache.RedisCache, nil)

	spans := exporter.GetSpans()
	qt.Assert(t, qt.HasLen(spans, 3))
	for i, want := range []string{"GET sessions", "SET sessions", "DELETE sessions"} {
		s := spans[i]
		qt.Check(t, qt.Equals(s.Name, want))
		a := attrs(s)
		qt.Check(t, qt.Equals(a["azugo.cache.backend"], string(cache.RedisCache)))
		qt.Check(t, qt.Equals(a["azugo.cache.instance"], "sessions"))
		qt.Check(t, qt.Equals(a["service.peer.name"], "cache"))
		_, keyed := a["azugo.cache.key"]
		qt.Check(t, qt.IsFalse(keyed), qt.Commentf("span %q carries the key attribute", s.Name))
		for _, text := range spanTexts(s) {
			qt.Check(t, qt.IsFalse(strings.Contains(text, secretPart)),
				qt.Commentf("span %q carries the key in %q", s.Name, text))
		}
	}
}

// A failed cache operation still marks its span as failed, as the framework's
// own cache tracing does.
func TestAFailedCacheSpanIsMarkedFailed(t *testing.T) {
	app, exporter := tracedApp(t)

	observe(app, cache.InstrumentationSet, cache.RedisCache, errors.New("connection reset"))

	spans := exporter.GetSpans()
	qt.Assert(t, qt.HasLen(spans, 1))
	qt.Check(t, qt.Equals(spans[0].Status.Code, codes.Error))
	qt.Check(t, qt.Equals(spans[0].Status.Description, "connection reset"))
}

// The in-memory cache records no span, as the framework's own cache tracing does.
func TestAnInMemoryCacheRecordsNoSpan(t *testing.T) {
	app, exporter := tracedApp(t)

	observe(app, cache.InstrumentationSet, cache.MemoryCache, nil)

	qt.Check(t, qt.HasLen(exporter.GetSpans(), 0))
}

// A service's own span name formatter still names the cache spans.
func TestAServicesSpanNameFormatterStillNamesCacheSpans(t *testing.T) {
	format := opentelemetry.InstrumentationSpanNameFormatter(func(_ context.Context, op string, _ ...any) string {
		if op == cache.InstrumentationSet {
			return "store write"
		}

		return ""
	})
	app, exporter := tracedApp(t, format)

	observe(app, cache.InstrumentationSet, cache.RedisCache, nil)

	spans := exporter.GetSpans()
	qt.Assert(t, qt.HasLen(spans, 1))
	qt.Check(t, qt.Equals(spans[0].Name, "store write"))
}

// A service's own recorder for the cache's operations, and a catch-all one, are
// never called for them: the kit's recorder answers every one first.
func TestAServicesOwnCacheRecorderIsNotCalled(t *testing.T) {
	calls := 0
	own := func(context.Context, trace.Tracer, propagation.TextMapPropagator, opentelemetry.InstrumentationSpanNameFormatter, string, ...any) (func(error), bool) {
		calls++

		return func(error) {}, true
	}
	app, exporter := tracedApp(t,
		opentelemetry.InstrumentationRecorder("own-cache", own, cache.InstrumentationSet),
		opentelemetry.InstrumentationRecorder("catch-all", own))

	observe(app, cache.InstrumentationSet, cache.RedisCache, nil)
	observe(app, cache.InstrumentationSet, cache.MemoryCache, nil)

	qt.Check(t, qt.Equals(calls, 0))
	qt.Check(t, qt.HasLen(exporter.GetSpans(), 1), qt.Commentf("the kit's span for the Redis write, none for memory"))
}

// The cache's read and hit counts are kept as the framework counts them: a read
// counts one get, a hit served from memory counts one hit and records no span.
func TestCacheReadsAndHitsAreStillCounted(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	meters := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = meters.Shutdown(context.Background()) })

	exporter := tracetest.NewInMemoryExporter()
	tracer := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter)).Tracer("test")
	record := cacheRecorder(func() metric.MeterProvider { return meters })
	noName := func(context.Context, string, ...any) string { return "" }

	finish, handled := record(context.Background(), tracer, nil, noName, cache.InstrumentationGet, sessionKey, string(cache.RedisCache), "sessions")
	qt.Assert(t, qt.IsTrue(handled))
	finish(nil)
	finish, handled = record(context.Background(), tracer, nil, noName, cache.InstrumentationGetHit, sessionKey, string(cache.RedisCache), "sessions")
	qt.Assert(t, qt.IsTrue(handled))
	finish(nil)

	var rm metricdata.ResourceMetrics
	qt.Assert(t, qt.IsNil(reader.Collect(context.Background(), &rm)))
	counts := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		qt.Check(t, qt.Equals(sm.Scope.Name, opentelemetry.ScopeName+"/cache"))
		for _, m := range sm.Metrics {
			if sum, ok := m.Data.(metricdata.Sum[int64]); ok {
				for _, dp := range sum.DataPoints {
					counts[m.Name] += dp.Value
				}
			}
		}
	}
	qt.Check(t, qt.Equals(counts["azugo.cache.gets"], int64(1)))
	qt.Check(t, qt.Equals(counts["azugo.cache.hits"], int64(1)))
	qt.Check(t, qt.HasLen(exporter.GetSpans(), 1), qt.Commentf("the read records a span, the hit does not"))
}
