package observability

import (
	"fmt"
	"sort"
	"strings"

	"github.com/VictoriaMetrics/metrics"
)

// Standard metric label keys, injected so dashboards are uniform across the
// fleet. Azugo already emits the inbound-request
// RED metrics (requests_total / request_duration_seconds with code/method/path
// labels); these helpers cover the deltas go-platform-kit owns — broker
// publish/consume and outbound HTTP-client calls.
const (
	LabelService = "service"
	LabelRoute   = "route"
	LabelOutcome = "outcome"
	LabelTopic   = "topic"
	LabelTarget  = "target"
)

// Standard outcome label values.
const (
	OutcomeSuccess = "success"
	OutcomeError   = "error"
)

// Standard metric names for the go-platform-kit-owned signals.
const (
	MetricBrokerPublishTotal      = "platform_broker_publish_total"
	MetricBrokerConsumeTotal      = "platform_broker_consume_total"
	MetricOutboundRequestsTotal   = "platform_http_client_requests_total"
	MetricOutboundDurationSeconds = "platform_http_client_request_duration_seconds"
)

// IncCounter increments the named counter with the given labels, registering it
// on first use (VictoriaMetrics/metrics — the same registry Azugo exposes at
// /metrics). Label VALUES must be low-cardinality (service names, topics,
// outcomes — never ids, urls, or user input): every distinct label set creates
// a new time series.
func IncCounter(name string, labels map[string]string) {
	metrics.GetOrCreateCounter(name + formatLabels(labels)).Inc()
}

// ObserveSeconds records a duration (in seconds) on the named histogram with the
// given labels, registering it on first use.
func ObserveSeconds(name string, labels map[string]string, seconds float64) {
	metrics.GetOrCreateHistogram(name + formatLabels(labels)).Update(seconds)
}

// formatLabels renders a label set in VictoriaMetrics/Prometheus form —
// `{key="value",…}` — with keys sorted so identical label sets always produce
// the same metric series. Values are quoted/escaped via %q.
func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}

	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	var b strings.Builder

	b.WriteByte('{')

	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}

		fmt.Fprintf(&b, "%s=%q", k, labels[k])
	}

	b.WriteByte('}')

	return b.String()
}
