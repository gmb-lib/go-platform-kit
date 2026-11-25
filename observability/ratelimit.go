package observability

import (
	"context"

	"azugo.io/core/instrumenter"
	"azugo.io/core/ratelimit"
	"azugo.io/opentelemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

// rateLimitTracing is the rate limiter's tracing as the framework records it,
// named without the key. The framework names each span "<OPERATION> <key>", and
// a limiter's key is a client's address or a person's subject, personal data in
// the trace store. Registered, like cacheTracing, under the framework's name and
// before it, it takes every rate-limit operation in the framework's place: the
// span named by the operation alone (or by a service's formatter), with the
// limiter's answer, never the key.
func rateLimitTracing() opentelemetry.Option {
	return opentelemetry.InstrumentationRecorder("ratelimit", rateLimitRecorder,
		ratelimit.InstrumentationAllow,
		ratelimit.InstrumentationPeek,
		ratelimit.InstrumentationWait,
		ratelimit.InstrumentationReset,
	)
}

// rateLimitRecorder records a rate-limit operation as the framework does
// (azugo.io/opentelemetry v0.50.0 ratelimit.go), named without the key. Every
// operation registered for it is answered as handled, so the framework's own
// recorder never takes one.
func rateLimitRecorder(ctx context.Context, tr trace.Tracer, _ propagation.TextMapPropagator, spfmt opentelemetry.InstrumentationSpanNameFormatter, op string, args ...any) (func(error), bool) {
	var (
		name string
		res  *ratelimit.Result
		ok   bool
	)
	switch op {
	case ratelimit.InstrumentationAllow:
		_, res, ok = ratelimit.InstrAllow(op, args...)
		name = "ALLOW"
	case ratelimit.InstrumentationPeek:
		_, res, ok = ratelimit.InstrPeek(op, args...)
		name = "PEEK"
	case ratelimit.InstrumentationWait:
		_, ok = ratelimit.InstrWait(op, args...)
		name = "WAIT"
	case ratelimit.InstrumentationReset:
		_, ok = ratelimit.InstrReset(op, args...)
		name = "RESET"
	}
	if !ok || !opentelemetry.Recording(ctx) {
		return instrumenter.NullFinish, true
	}

	if spfmt != nil {
		if custom := spfmt(ctx, op, args...); custom != "" {
			name = custom
		}
	}

	_, span := opentelemetry.StartSpan(ctx, tr, name,
		trace.WithAttributes(semconv.ServicePeerName("ratelimit")),
		trace.WithSpanKind(trace.SpanKindInternal))

	return func(err error) {
		if res != nil {
			span.SetAttributes(
				attribute.Bool("ratelimit.allowed", res.Allowed),
				attribute.Int("ratelimit.remaining", res.Remaining),
			)
			if res.RetryAfter > 0 {
				span.SetAttributes(attribute.Int64("ratelimit.retry_after_ms", res.RetryAfter.Milliseconds()))
			}
		}
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			span.RecordError(err, trace.WithStackTrace(true))
		}
		span.End()
	}, true
}
