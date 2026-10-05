package otel

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"

	"github.com/convoy-road-trips-app/stats"
)

const droppedLabelsMetric = "telemetry_dropped_labels_total"

// wireAttributes returns the attribute sets (key -> string value) of every
// sum and histogram datapoint named name that reached the receiver.
func (r *metricsReceiver) wireAttributes(name string) []map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var sets []map[string]string
	for _, request := range r.log {
		for _, resource := range request.GetResourceMetrics() {
			for _, scope := range resource.GetScopeMetrics() {
				for _, m := range scope.GetMetrics() {
					if m.GetName() != name {
						continue
					}
					for _, p := range m.GetSum().GetDataPoints() {
						sets = append(sets, keyValues(p.GetAttributes()))
					}
					for _, p := range m.GetHistogram().GetDataPoints() {
						sets = append(sets, keyValues(p.GetAttributes()))
					}
				}
			}
		}
	}
	return sets
}

// droppedTotals returns the latest cumulative drop counter value per reason.
func (r *metricsReceiver) droppedTotals() map[string]float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	totals := map[string]float64{}
	for _, request := range r.log {
		for _, resource := range request.GetResourceMetrics() {
			for _, scope := range resource.GetScopeMetrics() {
				for _, m := range scope.GetMetrics() {
					if m.GetName() != droppedLabelsMetric {
						continue
					}
					for _, p := range m.GetSum().GetDataPoints() {
						totals[keyValues(p.GetAttributes())["reason"]] = p.GetAsDouble()
					}
				}
			}
		}
	}
	return totals
}

func keyValues(attrs []*commonpb.KeyValue) map[string]string {
	kv := make(map[string]string, len(attrs))
	for _, a := range attrs {
		kv[a.GetKey()] = a.GetValue().GetStringValue()
	}
	return kv
}

func TestOTelInstruments_export_dotted_semantic_attribute_keys_unchanged(t *testing.T) {
	// Given: OTel API instruments exporting to a real OTLP/HTTP receiver
	receiver := newMetricsReceiver(t)
	provider := receiver.provider(t)
	meter := provider.Meter("semconv")
	counter, err := meter.Int64Counter("http_requests_total")
	require.NoError(t, err)
	histogram, err := meter.Float64Histogram("http_server_duration_seconds")
	require.NoError(t, err)
	attrs := metric.WithAttributes(attribute.String("http.method", "GET"), attribute.String("http.route", "/orders/{id}"))

	// When
	counter.Add(context.Background(), 1, attrs)
	histogram.Record(context.Background(), 0.042, attrs)
	require.NoError(t, provider.ForceFlush(context.Background()))

	// Then: the protobuf datapoints carry exactly the dotted keys, and nothing was dropped
	want := []map[string]string{{"http.method": "GET", "http.route": "/orders/{id}"}}
	require.Equal(t, want, receiver.wireAttributes("http_requests_total"))
	require.Equal(t, want, receiver.wireAttributes("http_server_duration_seconds"))
	require.Empty(t, receiver.droppedTotals())
}

func TestOTelCounter_rejects_malformed_dotted_key_without_using_the_series_slot(t *testing.T) {
	// Given: room for exactly one series
	receiver := newMetricsReceiver(t)
	provider := receiver.provider(t, WithStatsOptions(stats.WithMaxCardinality(1)))
	counter, err := provider.Meter("semconv").Int64Counter("http_requests_total")
	require.NoError(t, err)
	counter.Add(context.Background(), 1, metric.WithAttributes(attribute.String("bad..key", "x")))

	// When: a valid dotted series follows the malformed one
	counter.Add(context.Background(), 1, metric.WithAttributes(attribute.String("http.method", "GET")))
	require.NoError(t, provider.ForceFlush(context.Background()))

	// Then: only the valid series is exported, and no D10 drop is counted
	require.Equal(t, []map[string]string{{"http.method": "GET"}}, receiver.wireAttributes("http_requests_total"))
	require.Empty(t, receiver.droppedTotals())
}

func TestOTelCounter_exports_2000_dotted_key_series_and_counts_the_2001st(t *testing.T) {
	// Given: the default 2000-series limit
	receiver := newMetricsReceiver(t)
	provider := receiver.provider(t)
	counter, err := provider.Meter("semconv").Int64Counter("http_requests_total")
	require.NoError(t, err)

	// When: 2001 distinct http.route values arrive
	for i := range 2001 {
		counter.Add(context.Background(), 1, metric.WithAttributes(attribute.String("http.route", "/r/"+strconv.Itoa(i))))
	}
	require.NoError(t, provider.ForceFlush(context.Background()))

	// Then
	routes := map[string]struct{}{}
	for _, set := range receiver.wireAttributes("http_requests_total") {
		routes[set["http.route"]] = struct{}{}
	}
	require.Len(t, routes, 2000)
	require.NotContains(t, routes, "/r/2000")
	require.Equal(t, map[string]float64{"series_limit": 1}, receiver.droppedTotals())
}

func TestOTelHistogram_trims_dotted_keys_to_10_and_caps_values_at_256(t *testing.T) {
	// Given: 12 dotted keys; the lexically first one has a 300-rune value
	receiver := newMetricsReceiver(t)
	provider := receiver.provider(t)
	histogram, err := provider.Meter("semconv").Float64Histogram("http_server_duration_seconds")
	require.NoError(t, err)
	kvs := make([]attribute.KeyValue, 0, 12)
	kvs = append(kvs, attribute.String("a.long", strings.Repeat("é", 300)))
	for _, k := range []string{"x.l", "x.c", "x.k", "x.j", "x.e", "x.b", "x.i", "x.d", "x.h", "x_a", "x.f"} {
		kvs = append(kvs, attribute.String(k, "v"))
	}

	// When
	histogram.Record(context.Background(), 0.1, metric.WithAttributes(kvs...))
	require.NoError(t, provider.ForceFlush(context.Background()))

	// Then: first 10 lexical keys survive, the value is capped, and 2 labels are counted
	sets := receiver.wireAttributes("http_server_duration_seconds")
	require.Len(t, sets, 1)
	keys := make([]string, 0, len(sets[0]))
	for k := range sets[0] {
		keys = append(keys, k)
	}
	require.ElementsMatch(t, []string{"a.long", "x.b", "x.c", "x.d", "x.e", "x.f", "x.h", "x.i", "x.j", "x.k"}, keys)
	require.Equal(t, 256, utf8.RuneCountInString(sets[0]["a.long"]))
	require.Equal(t, map[string]float64{"label_limit": 2}, receiver.droppedTotals())
}

func TestOTelCounterGetsContextTags(t *testing.T) {
	// Given: a context carrying tags and a counter from the OTel bridge
	receiver := newMetricsReceiver(t)
	provider := receiver.provider(t)
	counter, err := provider.Meter("ctx").Int64Counter("ctx_requests_total")
	require.NoError(t, err)
	ctx := stats.ContextWithTags(context.Background(),
		attribute.String("region", "eu"), attribute.String("tier", "gold"))

	// When: an explicit attribute overrides one of them
	counter.Add(ctx, 1, metric.WithAttributes(attribute.String("tier", "silver")))
	require.NoError(t, provider.ForceFlush(context.Background()))

	// Then: the exported series carries the context tag and the override
	require.Equal(t, []map[string]string{{"region": "eu", "tier": "silver"}}, receiver.wireAttributes("ctx_requests_total"))
}
