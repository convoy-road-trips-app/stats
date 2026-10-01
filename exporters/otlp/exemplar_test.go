package otlp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/convoy-road-trips-app/stats/models"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace"
	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

var (
	traceA = trace.TraceID{0xa1, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xab, 0xac, 0xad, 0xae, 0xaf, 0xb0}
	spanA  = trace.SpanID{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	traceB = trace.TraceID{0xb1, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6, 0xb7, 0xb8, 0xb9, 0xba, 0xbb, 0xbc, 0xbd, 0xbe, 0xbf, 0xc0}
	spanB  = trace.SpanID{0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18}
)

func sampled(m *models.Metric, traceID trace.TraceID, spanID trace.SpanID) *models.Metric {
	m.TraceID, m.SpanID = traceID, spanID
	return m
}

func histogramPoint(t *testing.T, rm metricdata.ResourceMetrics, name string) metricdata.HistogramDataPoint[float64] {
	t.Helper()
	points := metricByName(t, rm, name).Data.(metricdata.Histogram[float64]).DataPoints
	require.Len(t, points, 1)
	return points[0]
}

func sumPoint(t *testing.T, rm metricdata.ResourceMetrics, name string) metricdata.DataPoint[float64] {
	t.Helper()
	points := metricByName(t, rm, name).Data.(metricdata.Sum[float64]).DataPoints
	require.Len(t, points, 1)
	return points[0]
}

func TestExporter_histogram_keeps_latest_sampled_exemplar_per_bucket(t *testing.T) {
	// Given: bounds 1, 10 — two sampled observations in bucket (1,10], one in (10,+Inf), one unsampled
	start := time.Unix(100, 0)
	collector := &collectingExporter{}
	exporter := &Exporter{config: &models.OTLPConfig{Enabled: true, HistogramBuckets: []float64{1, 10}}, otlpExporter: collector}
	batch := []*models.Metric{
		sampled(&models.Metric{Name: "latency", Type: models.MetricTypeHistogram, Value: 2, Timestamp: start}, traceA, spanA),
		sampled(&models.Metric{Name: "latency", Type: models.MetricTypeHistogram, Value: 5, Timestamp: start.Add(time.Second)}, traceB, spanB),
		sampled(&models.Metric{Name: "latency", Type: models.MetricTypeHistogram, Value: 50, Timestamp: start}, traceA, spanB),
		{Name: "latency", Type: models.MetricTypeHistogram, Value: 0.5, Timestamp: start},
	}

	// When
	require.NoError(t, exporter.Export(context.Background(), batch))

	// Then: one exemplar per bucket that saw a sampled observation, in bucket order
	point := histogramPoint(t, collector.collections[0], "latency")
	require.Equal(t, uint64(4), point.Count)
	require.Equal(t, []metricdata.Exemplar[float64]{
		{Time: start.Add(time.Second), Value: 5, TraceID: traceB[:], SpanID: spanB[:]},
		{Time: start, Value: 50, TraceID: traceA[:], SpanID: spanB[:]},
	}, point.Exemplars)
}

func TestExporter_counter_keeps_sampled_exemplar_when_later_observation_is_unsampled(t *testing.T) {
	// Given: two observations of one series in one batch, only the first sampled
	start := time.Unix(100, 0)
	collector := &collectingExporter{}
	exporter := &Exporter{config: &models.OTLPConfig{Enabled: true}, otlpExporter: collector}
	batch := []*models.Metric{
		sampled(&models.Metric{Name: "requests_total", Type: models.MetricTypeCounter, Value: 2, Timestamp: start}, traceA, spanA),
		{Name: "requests_total", Type: models.MetricTypeCounter, Value: 3, Timestamp: start.Add(time.Second)},
	}

	// When
	require.NoError(t, exporter.Export(context.Background(), batch))

	// Then
	point := sumPoint(t, collector.collections[0], "requests_total")
	require.Equal(t, float64(5), point.Value)
	require.Equal(t, []metricdata.Exemplar[float64]{{Time: start, Value: 2, TraceID: traceA[:], SpanID: spanA[:]}}, point.Exemplars)
}

func TestExporter_cumulative_exports_do_not_repeat_previous_interval_exemplars(t *testing.T) {
	// Given: a first export with sampled counter and histogram observations
	start := time.Unix(100, 0)
	collector := &collectingExporter{}
	exporter := &Exporter{config: &models.OTLPConfig{Enabled: true}, otlpExporter: collector}
	require.NoError(t, exporter.Export(context.Background(), []*models.Metric{
		sampled(&models.Metric{Name: "requests_total", Type: models.MetricTypeCounter, Value: 2, Timestamp: start}, traceA, spanA),
		sampled(&models.Metric{Name: "latency", Type: models.MetricTypeHistogram, Value: 0.2, Timestamp: start}, traceA, spanA),
	}))

	// When: the next interval has only unsampled observations
	require.NoError(t, exporter.Export(context.Background(), []*models.Metric{
		{Name: "requests_total", Type: models.MetricTypeCounter, Value: 3, Timestamp: start.Add(time.Second)},
		{Name: "latency", Type: models.MetricTypeHistogram, Value: 0.3, Timestamp: start.Add(time.Second)},
	}))

	// Then: totals stay cumulative but exemplars belong to their own interval
	require.Len(t, collector.collections, 2)
	require.Len(t, sumPoint(t, collector.collections[0], "requests_total").Exemplars, 1)
	require.Len(t, histogramPoint(t, collector.collections[0], "latency").Exemplars, 1)
	second := sumPoint(t, collector.collections[1], "requests_total")
	require.Equal(t, float64(5), second.Value)
	require.Empty(t, second.Exemplars)
	histogram := histogramPoint(t, collector.collections[1], "latency")
	require.Equal(t, uint64(2), histogram.Count)
	require.Empty(t, histogram.Exemplars)
}

func TestExporter_exemplar_ids_survive_reuse_of_the_pooled_metric(t *testing.T) {
	// Given: a pooled metric that is exported and then reused for another span
	collector := &collectingExporter{}
	exporter := &Exporter{config: &models.OTLPConfig{Enabled: true}, otlpExporter: collector}
	m := models.AcquireMetric()
	m.Name, m.Type, m.Value, m.Timestamp = "requests_total", models.MetricTypeCounter, 1, time.Unix(100, 0)
	m.Attributes = append(m.Attributes, attribute.String("route", "a"))
	sampled(m, traceA, spanA)
	require.NoError(t, exporter.Export(context.Background(), []*models.Metric{m}))

	// When
	models.ReleaseMetric(m)
	sampled(m, traceB, spanB)

	// Then
	exemplar := sumPoint(t, collector.collections[0], "requests_total").Exemplars[0]
	require.Equal(t, traceA[:], exemplar.TraceID)
	require.Equal(t, spanA[:], exemplar.SpanID)
}

func TestExporter_gauge_and_unsampled_points_have_no_exemplars(t *testing.T) {
	// Given
	collector := &collectingExporter{}
	exporter := &Exporter{config: &models.OTLPConfig{Enabled: true}, otlpExporter: collector}

	// When
	require.NoError(t, exporter.Export(context.Background(), []*models.Metric{
		sampled(&models.Metric{Name: "inflight", Type: models.MetricTypeGauge, Value: 1, Timestamp: time.Unix(100, 0)}, traceA, spanA),
		{Name: "requests_total", Type: models.MetricTypeCounter, Value: 1, Timestamp: time.Unix(100, 0)},
	}))

	// Then
	gauge := metricByName(t, collector.collections[0], "inflight").Data.(metricdata.Gauge[float64])
	require.Empty(t, gauge.DataPoints[0].Exemplars)
	require.Empty(t, sumPoint(t, collector.collections[0], "requests_total").Exemplars)
}

func TestExporter_OTLPHTTP_serializes_exemplar_trace_and_span_ids_as_bytes(t *testing.T) {
	// Given: a real OTLP/HTTP receiver
	received := make(chan *collectormetricspb.ExportMetricsServiceRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	exporter, err := NewExporter(&models.OTLPConfig{
		Enabled: true, Endpoint: strings.TrimPrefix(server.URL, "http://"), Insecure: true, Protocol: models.OTLPProtocolHTTP,
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, exporter.Shutdown(context.Background())) }()

	// When
	require.NoError(t, exporter.Export(context.Background(), []*models.Metric{
		sampled(&models.Metric{Name: "latency", Type: models.MetricTypeHistogram, Value: 0.2, Timestamp: time.Now()}, traceA, spanA),
		sampled(&models.Metric{Name: "requests_total", Type: models.MetricTypeCounter, Value: 4, Timestamp: time.Now()}, traceB, spanB),
	}))

	// Then
	request := <-received
	histogram := wireMetric(t, request, "latency").GetHistogram().GetDataPoints()[0].GetExemplars()
	require.Len(t, histogram, 1)
	require.Equal(t, traceA[:], histogram[0].GetTraceId())
	require.Equal(t, spanA[:], histogram[0].GetSpanId())
	require.Equal(t, 0.2, histogram[0].GetAsDouble())
	counter := wireMetric(t, request, "requests_total").GetSum().GetDataPoints()[0].GetExemplars()
	require.Len(t, counter, 1)
	require.Equal(t, traceB[:], counter[0].GetTraceId())
	require.Equal(t, spanB[:], counter[0].GetSpanId())
	require.Equal(t, float64(4), counter[0].GetAsDouble())
}
