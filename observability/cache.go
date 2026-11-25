package observability

import (
	"context"
	"sync"

	"azugo.io/core/cache"
	"azugo.io/core/instrumenter"
	"azugo.io/opentelemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

// cacheTracing is the cache's tracing as the framework records it, without the
// key. The framework's own cache tracing (azugo.io/opentelemetry) puts the whole
// key on every cache span, and a key is often a credential: a session id, a
// one-time sign-in code, a refresh-token hash. A caller's recorders are tried
// before the framework's, and the first one that handles an operation records
// it, so this recorder, registered under the framework's name "cache", takes
// every cache operation in its place: the same span name, attributes, status and
// counts, never the key.
func cacheTracing(meters func() metric.MeterProvider) opentelemetry.Option {
	return opentelemetry.InstrumentationRecorder("cache", cacheRecorder(meters),
		cache.InstrumentationGet,
		cache.InstrumentationGetHit,
		cache.InstrumentationSet,
		cache.InstrumentationDelete,
	)
}

// cacheCounters are the framework's two cache counters, made on first use from
// the meter provider in force then (the one tracing set up).
type cacheCounters struct {
	once   sync.Once
	meters func() metric.MeterProvider
	gets   metric.Int64Counter
	hits   metric.Int64Counter
}

func (c *cacheCounters) init() {
	c.once.Do(func() {
		meter := c.meters().Meter(opentelemetry.ScopeName+"/cache",
			metric.WithInstrumentationVersion(opentelemetry.Version()))

		var err error
		if c.gets, err = meter.Int64Counter("azugo.cache.gets",
			metric.WithDescription("Cache get operations."),
			metric.WithUnit("{operation}")); err != nil {
			otel.Handle(err)
		}
		if c.hits, err = meter.Int64Counter("azugo.cache.hits",
			metric.WithDescription("Cache get operations served from in-process memory."),
			metric.WithUnit("{operation}")); err != nil {
			otel.Handle(err)
		}
	})
}

// cacheAttrs are a cache event's backend and instance, never its key.
func cacheAttrs(args ...any) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, 3)
	if backend, ok := cache.InstrBackend(args...); ok {
		attrs = append(attrs, attribute.String("azugo.cache.backend", string(backend)))
	}
	if instance, ok := cache.InstrInstance(args...); ok {
		attrs = append(attrs, attribute.String("azugo.cache.instance", instance))
	}

	return attrs
}

func count(ctx context.Context, counter metric.Int64Counter, args ...any) {
	if counter == nil {
		return
	}
	counter.Add(ctx, 1, metric.WithAttributes(cacheAttrs(args...)...))
}

// cacheRecorder records a cache operation as the framework does (azugo.io/opentelemetry
// v0.50.0 cache.go), leaving out the key.
func cacheRecorder(meters func() metric.MeterProvider) opentelemetry.InstrumentationRecorderFunc {
	counters := &cacheCounters{meters: meters}

	return func(ctx context.Context, tr trace.Tracer, _ propagation.TextMapPropagator, spfmt opentelemetry.InstrumentationSpanNameFormatter, op string, args ...any) (func(error), bool) {
		counters.init()

		var (
			method string
			ok     bool
		)
		switch op {
		case cache.InstrumentationGetHit:
			// Hits are counted; a span per in-process hit would be noise.
			count(ctx, counters.hits, args...)

			return instrumenter.NullFinish, true
		case cache.InstrumentationGet:
			count(ctx, counters.gets, args...)
			_, ok = cache.InstrGet(op, args...)
			method = "GET "
		case cache.InstrumentationSet:
			_, ok = cache.InstrSet(op, args...)
			method = "SET "
		case cache.InstrumentationDelete:
			_, ok = cache.InstrDelete(op, args...)
			method = "DELETE "
		}
		// Every operation registered here is answered as handled, a span or none,
		// so the framework's own cache recorder, which would add the key, never
		// takes one.
		if !ok {
			return instrumenter.NullFinish, true
		}
		if backend, ok := cache.InstrBackend(args...); ok && backend == cache.MemoryCache {
			return instrumenter.NullFinish, true
		}
		if !opentelemetry.Recording(ctx) {
			return instrumenter.NullFinish, true
		}

		name := ""
		if spfmt != nil {
			name = spfmt(ctx, op, args...)
		}
		if name == "" {
			instance, _ := cache.InstrInstance(args...)
			name = method + instance
		}

		_, span := opentelemetry.StartSpan(ctx, tr, name,
			trace.WithAttributes(append(cacheAttrs(args...), semconv.ServicePeerName("cache"))...),
			trace.WithSpanKind(trace.SpanKindInternal))

		return func(err error) {
			if err != nil {
				span.SetStatus(codes.Error, err.Error())
				span.RecordError(err, trace.WithStackTrace(true))
			}
			span.End()
		}, true
	}
}
