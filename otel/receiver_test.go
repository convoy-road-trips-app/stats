package otel

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/convoy-road-trips-app/stats"
	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// wirePoint is the latest datapoint a real OTLP/HTTP receiver got for one series.
type wirePoint struct {
	kind      string // "sum", "gauge", "histogram" or "exponential"
	monotonic bool
	value     float64
	exemplars []*metricspb.Exemplar
}

// metricsReceiver is a real OTLP/HTTP receiver on an ephemeral port that keeps
// the latest datapoint per metric name and "pool" attribute value.
type metricsReceiver struct {
	server   *httptest.Server
	mu       sync.Mutex
	points   map[string]wirePoint
	log      []*collectormetricspb.ExportMetricsServiceRequest // every request, in arrival order
	requests chan struct{}
}

func newMetricsReceiver(t *testing.T) *metricsReceiver {
	t.Helper()
	r := &metricsReceiver{points: map[string]wirePoint{}, requests: make(chan struct{}, 1024)}
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
		select {
		case r.requests <- struct{}{}:
		default:
		}
	}))
	t.Cleanup(r.server.Close)
	return r
}

func (r *metricsReceiver) record(request *collectormetricspb.ExportMetricsServiceRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log = append(r.log, request)
	for _, resource := range request.GetResourceMetrics() {
		for _, scope := range resource.GetScopeMetrics() {
			for _, metric := range scope.GetMetrics() {
				name := metric.GetName()
				for _, p := range metric.GetSum().GetDataPoints() {
					r.points[seriesKey(name, p.GetAttributes())] = wirePoint{
						kind: "sum", monotonic: metric.GetSum().GetIsMonotonic(), value: p.GetAsDouble(), exemplars: p.GetExemplars(),
					}
				}
				for _, p := range metric.GetGauge().GetDataPoints() {
					r.points[seriesKey(name, p.GetAttributes())] = wirePoint{kind: "gauge", value: p.GetAsDouble(), exemplars: p.GetExemplars()}
				}
				for _, p := range metric.GetHistogram().GetDataPoints() {
					r.points[seriesKey(name, p.GetAttributes())] = wirePoint{kind: "histogram", value: p.GetSum(), exemplars: p.GetExemplars()}
				}
				for _, p := range metric.GetExponentialHistogram().GetDataPoints() {
					r.points[seriesKey(name, p.GetAttributes())] = wirePoint{kind: "exponential", value: p.GetSum(), exemplars: p.GetExemplars()}
				}
			}
		}
	}
}

func seriesKey(name string, attrs []*commonpb.KeyValue) string {
	for _, attr := range attrs {
		if attr.GetKey() == "pool" {
			return name + "{pool=" + attr.GetValue().GetStringValue() + "}"
		}
	}
	return name
}

// take returns and forgets the latest point of a series.
func (r *metricsReceiver) take(series string) (wirePoint, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	point, ok := r.points[series]
	delete(r.points, series)
	return point, ok
}

// await waits, bounded, until the receiver holds a point for series.
func (r *metricsReceiver) await(t *testing.T, series string) wirePoint {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		if point, ok := r.take(series); ok {
			return point
		}
		select {
		case <-r.requests:
		case <-deadline:
			t.Fatalf("series %s never reached the OTLP receiver", series)
		}
	}
}

// provider returns a MeterProvider exporting to r through one worker that
// exports only on ForceFlush/Shutdown unless opts add periodic collection.
// Full batches still export in the background, bounded by UDPTimeout, so the
// bound is raised from 100 ms to keep large tests independent of -race speed.
func (r *metricsReceiver) provider(t *testing.T, opts ...MeterProviderOption) *MeterProvider {
	t.Helper()
	all := append([]MeterProviderOption{WithStatsOptions(
		stats.WithServiceName("observable-e2e"),
		stats.WithVersionReporting(false),
		stats.WithWorkers(1),
		stats.WithFlushInterval(time.Hour),
		stats.WithUDPTimeout(10*time.Second),
		stats.WithOTLP(&stats.OTLPConfig{
			Endpoint: strings.TrimPrefix(r.server.URL, "http://"),
			Insecure: true,
			Protocol: stats.OTLPProtocolHTTP,
		}),
	)}, opts...)
	provider, err := NewMeterProvider(all...)
	if err != nil {
		t.Fatalf("create MeterProvider: %v", err)
	}
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	return provider
}
