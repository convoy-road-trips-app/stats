package stats

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

var (
	exemplarTraceID = trace.TraceID{0x4b, 0xf9, 0x2f, 0x35, 0x77, 0xb3, 0x4d, 0xa6, 0xa3, 0xce, 0x92, 0x9d, 0x0e, 0x0e, 0x47, 0x36}
	exemplarSpanID  = trace.SpanID{0x00, 0xf0, 0x67, 0xaa, 0x0b, 0xa9, 0x02, 0xb7}
)

// spanContext returns ctx carrying a remote span with the given sampled flag,
// exactly as a tracer propagates it to instrumentation.
func spanContext(sampled bool) context.Context {
	var flags trace.TraceFlags
	if sampled {
		flags = trace.FlagsSampled
	}
	return trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: exemplarTraceID, SpanID: exemplarSpanID, TraceFlags: flags, Remote: true,
	}))
}

// exemplarReceiver is a real OTLP/HTTP receiver that keeps every exemplar it
// receives, keyed by metric name and the value of the "route" attribute.
type exemplarReceiver struct {
	server    *httptest.Server
	mu        sync.Mutex
	exemplars map[string][]*metricspb.Exemplar
	points    map[string]int
}

func newExemplarReceiver(t *testing.T) *exemplarReceiver {
	t.Helper()
	r := &exemplarReceiver{exemplars: map[string][]*metricspb.Exemplar{}, points: map[string]int{}}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var request collectormetricspb.ExportMetricsServiceRequest
		if err := proto.Unmarshal(body, &request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		r.record(&request)
	}))
	t.Cleanup(r.server.Close)
	return r
}

func (r *exemplarReceiver) record(request *collectormetricspb.ExportMetricsServiceRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, resource := range request.GetResourceMetrics() {
		for _, scope := range resource.GetScopeMetrics() {
			for _, metric := range scope.GetMetrics() {
				for _, point := range metric.GetSum().GetDataPoints() {
					r.add(metric.GetName(), attributeValue(point.GetAttributes(), "route"), point.GetExemplars())
				}
				for _, point := range metric.GetGauge().GetDataPoints() {
					r.add(metric.GetName(), attributeValue(point.GetAttributes(), "route"), point.GetExemplars())
				}
				for _, point := range metric.GetHistogram().GetDataPoints() {
					r.add(metric.GetName(), attributeValue(point.GetAttributes(), "route"), point.GetExemplars())
				}
			}
		}
	}
}

func (r *exemplarReceiver) add(name, route string, exemplars []*metricspb.Exemplar) {
	key := name + "/" + route
	r.points[key]++
	r.exemplars[key] = append(r.exemplars[key], exemplars...)
}

func (r *exemplarReceiver) of(name, route string) (points int, exemplars []*metricspb.Exemplar) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := name + "/" + route
	return r.points[key], r.exemplars[key]
}

func (r *exemplarReceiver) option() Option {
	return WithOTLP(&OTLPConfig{
		Endpoint: strings.TrimPrefix(r.server.URL, "http://"),
		Insecure: true,
		Protocol: OTLPProtocolHTTP,
	})
}

func TestClient_OTLP_exports_trace_exemplars_only_for_counters_and_histograms_recorded_under_a_sampled_span(t *testing.T) {
	// Given: a client exporting to a real OTLP/HTTP receiver only on Flush
	receiver := newExemplarReceiver(t)
	client, err := NewClient(WithServiceName("exemplar-e2e"), WithFlushInterval(time.Hour), receiver.option())
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Shutdown(context.Background()) })
	contexts := map[string]context.Context{
		"sampled":   spanContext(true),
		"unsampled": spanContext(false),
		"none":      context.Background(),
	}
	for route, ctx := range contexts {
		require.NoError(t, client.Counter(ctx, "requests_total", 2, WithAttribute("route", route)))
		require.NoError(t, client.Histogram(ctx, "latency_seconds", 0.3, WithAttribute("route", route)))
		require.NoError(t, client.Gauge(ctx, "inflight", 7, WithAttribute("route", route)))
	}

	// When
	require.NoError(t, within(t, func() error { return client.Flush(context.Background()) }))

	// Then: sampled counter and histogram points carry the span as raw OTLP bytes
	for name, value := range map[string]float64{"requests_total": 2, "latency_seconds": 0.3} {
		points, exemplars := receiver.of(name, "sampled")
		require.Positive(t, points, name)
		require.Len(t, exemplars, 1, name)
		require.Equal(t, exemplarTraceID[:], exemplars[0].GetTraceId(), name)
		require.Equal(t, exemplarSpanID[:], exemplars[0].GetSpanId(), name)
		require.InDelta(t, value, exemplars[0].GetAsDouble(), 0.001, name)
		require.NotZero(t, exemplars[0].GetTimeUnixNano(), name)
		for _, route := range []string{"unsampled", "none"} {
			points, exemplars := receiver.of(name, route)
			require.Positive(t, points, name+"/"+route)
			require.Empty(t, exemplars, name+"/"+route)
		}
	}
	points, exemplars := receiver.of("inflight", "sampled")
	require.Positive(t, points)
	require.Empty(t, exemplars, "gauges carry no exemplars")
}
