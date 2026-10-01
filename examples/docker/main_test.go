package main

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
	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// receiver is an OTLP/HTTP endpoint that keeps every exported metric.
type receiver struct {
	mu      sync.Mutex
	metrics []*metricspb.Metric
}

func (r *receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var export collectormetricspb.ExportMetricsServiceRequest
	if err := proto.Unmarshal(body, &export); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rm := range export.GetResourceMetrics() {
		for _, sm := range rm.GetScopeMetrics() {
			r.metrics = append(r.metrics, sm.GetMetrics()...)
		}
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
}

func (r *receiver) named(name string) []*metricspb.Metric {
	r.mu.Lock()
	defer r.mu.Unlock()
	var found []*metricspb.Metric
	for _, m := range r.metrics {
		if m.GetName() == name {
			found = append(found, m)
		}
	}
	return found
}

func TestRun_exports_the_demo_metrics_over_OTLP_HTTP_and_drains_on_shutdown(t *testing.T) {
	// Given: an OTLP/HTTP receiver and a demo configured to export to it
	rcv := &receiver{}
	server := httptest.NewServer(rcv)
	defer server.Close()
	cfg := config{
		Endpoint: strings.TrimPrefix(server.URL, "http://"),
		Service:  "docker-example-test",
		Interval: 5 * time.Millisecond,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	// When: the demo runs until its context ends
	require.NoError(t, run(ctx, cfg))

	// Then: Shutdown drained the histogram with the default seconds bounds
	histograms := rcv.named(histogramName)
	require.NotEmpty(t, histograms, "no %s exported", histogramName)
	last := histograms[len(histograms)-1].GetHistogram()
	require.Equal(t, metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE, last.GetAggregationTemporality())
	point := last.GetDataPoints()[0]
	require.Equal(t, []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}, point.GetExplicitBounds())
	require.Positive(t, point.GetCount())
	require.NotEmpty(t, rcv.named(counterName), "no %s exported", counterName)
	require.NotEmpty(t, rcv.named(gaugeName), "no %s exported", gaugeName)
}

func TestConfigFromEnv_reads_the_endpoint_service_and_interval(t *testing.T) {
	// Given
	env := map[string]string{"OTLP_ENDPOINT": "collector:4318", "SERVICE_NAME": "demo", "INTERVAL": "250ms"}

	// When
	cfg, err := configFromEnv(func(key string) string { return env[key] })

	// Then
	require.NoError(t, err)
	require.Equal(t, config{Endpoint: "collector:4318", Service: "demo", Interval: 250 * time.Millisecond}, cfg)
}

func TestConfigFromEnv_defaults_to_a_local_collector(t *testing.T) {
	// When
	cfg, err := configFromEnv(func(string) string { return "" })

	// Then
	require.NoError(t, err)
	require.Equal(t, "localhost:4318", cfg.Endpoint)
	require.Equal(t, "stats-docker-example", cfg.Service)
	require.Equal(t, 200*time.Millisecond, cfg.Interval)
}

func TestConfigFromEnv_rejects_a_bad_interval(t *testing.T) {
	// When
	_, err := configFromEnv(func(key string) string {
		if key == "INTERVAL" {
			return "soon"
		}
		return ""
	})

	// Then
	require.Error(t, err)
}
