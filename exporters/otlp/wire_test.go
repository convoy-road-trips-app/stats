package otlp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/convoy-road-trips-app/stats/models"
	"github.com/stretchr/testify/require"
	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

func TestExporter_OTLPHTTP_cumulative_payload_across_exports(t *testing.T) {
	// Given: a real OTLP/HTTP receiver on an ephemeral port.
	received := make(chan *collectormetricspb.ExportMetricsServiceRequest, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/metrics" {
			http.Error(w, "unexpected OTLP path", http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var request collectormetricspb.ExportMetricsServiceRequest
		if err := proto.Unmarshal(body, &request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		received <- &request
	}))
	defer server.Close()
	t.Setenv("OTEL_SERVICE_NAME", "checkout-api")
	t.Setenv("DEPLOYMENT_ENVIRONMENT", "production")
	t.Setenv("SERVICE_VERSION", "2.4.1")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "team=payments,service.name=ignored")
	exporter, err := NewExporter(&models.OTLPConfig{
		Enabled: true, Endpoint: strings.TrimPrefix(server.URL, "http://"), Insecure: true,
		Protocol: models.OTLPProtocolHTTP,
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, exporter.Shutdown(context.Background())) }()
	start := time.Now()

	// When: separate batches pass through the real HTTP transport.
	require.NoError(t, exporter.Export(context.Background(), []*models.Metric{
		{Name: "requests_total", Type: models.MetricTypeCounter, Value: 2, Timestamp: start},
		{Name: "duration_seconds", Type: models.MetricTypeHistogram, Value: 0.01, Timestamp: start},
	}))
	require.NoError(t, exporter.Export(context.Background(), []*models.Metric{
		{Name: "requests_total", Type: models.MetricTypeCounter, Value: 3, Timestamp: start.Add(time.Second)},
		{Name: "duration_seconds", Type: models.MetricTypeHistogram, Value: 0.2, Timestamp: start.Add(time.Second)},
	}))

	// Then: the collector sees cumulative data and the required resource identity.
	first, second := <-received, <-received
	require.Equal(t, float64(2), wireMetric(t, first, "requests_total").GetSum().DataPoints[0].GetAsDouble())
	require.Equal(t, float64(5), wireMetric(t, second, "requests_total").GetSum().DataPoints[0].GetAsDouble())
	require.Equal(t, uint64(2), wireMetric(t, second, "duration_seconds").GetHistogram().DataPoints[0].Count)
	require.Equal(t, "checkout-api", wireResourceValue(t, second, "service.name"))
	require.Equal(t, "production", wireResourceValue(t, second, "deployment.environment"))
	require.Equal(t, "2.4.1", wireResourceValue(t, second, "service.version"))
	require.Equal(t, "payments", wireResourceValue(t, second, "team"))
	if path := os.Getenv("STATS_OTLP_QA_EVIDENCE"); path != "" {
		points := []map[string]any{}
		for _, request := range []*collectormetricspb.ExportMetricsServiceRequest{first, second} {
			counter := wireMetric(t, request, "requests_total").GetSum()
			histogram := wireMetric(t, request, "duration_seconds").GetHistogram()
			points = append(points, map[string]any{
				"temporality": counter.AggregationTemporality.String(),
				"counter":     counter.DataPoints[0].GetAsDouble(),
				"hist_count":  histogram.DataPoints[0].Count,
				"hist_sum":    histogram.DataPoints[0].GetSum(),
			})
		}
		resource := map[string]string{}
		for _, attr := range second.ResourceMetrics[0].Resource.Attributes {
			resource[attr.Key] = attr.Value.GetStringValue()
		}
		encoded, err := json.MarshalIndent(map[string]any{"resource": resource, "exports": points}, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, append(encoded, '\n'), 0o600))
	}
}

func wireMetric(t *testing.T, request *collectormetricspb.ExportMetricsServiceRequest, name string) *metricspb.Metric {
	t.Helper()
	for _, scope := range request.ResourceMetrics[0].ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name == name {
				return metric
			}
		}
	}
	t.Fatalf("metric %q missing from OTLP payload", name)
	return nil
}

func wireResourceValue(t *testing.T, request *collectormetricspb.ExportMetricsServiceRequest, key string) string {
	t.Helper()
	for _, attr := range request.ResourceMetrics[0].Resource.Attributes {
		if attr.Key == key {
			return attr.Value.GetStringValue()
		}
	}
	t.Fatalf("resource attribute %q missing from OTLP payload", key)
	return ""
}
