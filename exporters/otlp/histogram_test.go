package otlp

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/convoy-road-trips-app/stats/models"
)

func TestToResourceMetrics_HistogramProperties(t *testing.T) {
	// Given
	now := time.Now()
	metrics := []*models.Metric{{
		Name:      "hist",
		Type:      models.MetricTypeHistogram,
		Value:     250.5,
		Timestamp: now,
	}}

	// When
	rm := toResourceMetrics("svc", metrics)

	// Then
	histogram := rm.ScopeMetrics[0].Metrics[0].Data.(metricdata.Histogram[float64])
	assert.Equal(t, metricdata.CumulativeTemporality, histogram.Temporality)
	dp := histogram.DataPoints[0]
	assert.Equal(t, uint64(1), dp.Count)
	assert.InDelta(t, 250.5, dp.Sum, 0.001)
	assert.Equal(t, now, dp.Time)
	assert.Equal(t, models.DefaultHistogramBuckets(), dp.Bounds)
	assert.Equal(t, []uint64{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}, dp.BucketCounts)
}

func TestToResourceMetrics_HistogramAggregatesObservations(t *testing.T) {
	// Given
	now := time.Now()
	metrics := make([]*models.Metric, 1000)
	for i := range metrics {
		metrics[i] = &models.Metric{
			Name:  "request.duration",
			Type:  models.MetricTypeHistogram,
			Value: float64(i % 4),
			Attributes: []attribute.KeyValue{
				attribute.String("route", "/api"),
			},
			Timestamp: now,
		}
	}
	bounds := []float64{0.5, 1.5, 2.5}

	// When
	rm := toResourceMetricsWithBuckets("svc", metrics, bounds)

	// Then
	gotMetrics := rm.ScopeMetrics[0].Metrics
	require.Len(t, gotMetrics, 1)
	histogram, ok := gotMetrics[0].Data.(metricdata.Histogram[float64])
	require.True(t, ok)
	require.Len(t, histogram.DataPoints, 1)
	dp := histogram.DataPoints[0]
	assert.Equal(t, uint64(1000), dp.Count)
	assert.InDelta(t, 1500.0, dp.Sum, 0.001)
	assert.Equal(t, bounds, dp.Bounds)
	assert.Equal(t, []uint64{250, 250, 250, 250}, dp.BucketCounts)
	assert.Equal(t, metricdata.NewExtrema(0.0), dp.Min)
	assert.Equal(t, metricdata.NewExtrema(3.0), dp.Max)
	assert.Equal(t, now, dp.Time)
}

func TestToResourceMetrics_HistogramGroupsAttributeSets(t *testing.T) {
	// Given
	metrics := []*models.Metric{
		{Name: "latency", Type: models.MetricTypeHistogram, Value: 1, Attributes: []attribute.KeyValue{attribute.String("route", "/a")}},
		{Name: "latency", Type: models.MetricTypeHistogram, Value: 2, Attributes: []attribute.KeyValue{attribute.String("route", "/b")}},
		{Name: "latency", Type: models.MetricTypeHistogram, Value: 3, Attributes: []attribute.KeyValue{attribute.String("route", "/a")}},
	}

	// When
	rm := toResourceMetricsWithBuckets("svc", metrics, []float64{1.5, 2.5})

	// Then
	histogram := rm.ScopeMetrics[0].Metrics[0].Data.(metricdata.Histogram[float64])
	require.Len(t, histogram.DataPoints, 2)
	assert.Equal(t, uint64(2), histogram.DataPoints[0].Count)
	assert.InDelta(t, 4.0, histogram.DataPoints[0].Sum, 0.001)
	assert.Equal(t, []uint64{1, 0, 1}, histogram.DataPoints[0].BucketCounts)
	assert.Equal(t, uint64(1), histogram.DataPoints[1].Count)
	assert.Equal(t, []uint64{0, 1, 0}, histogram.DataPoints[1].BucketCounts)
}

func TestOTLPConfig_Validate_rejects_empty_histogram_buckets(t *testing.T) {
	// Given
	config := models.OTLPConfig{
		Enabled:          true,
		Endpoint:         "localhost:4317",
		HistogramBuckets: []float64{},
	}

	// When
	err := config.Validate()

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "histogram buckets must not be empty")
}

var benchmarkHistogramResourceMetrics metricdata.ResourceMetrics

func BenchmarkOTLPHistogramExport(b *testing.B) {
	const observations = 2048
	metrics := make([]*models.Metric, observations)
	for i := range metrics {
		metrics[i] = &models.Metric{
			Name:  "request.duration",
			Type:  models.MetricTypeHistogram,
			Value: float64(i%100) / 100,
			Attributes: []attribute.KeyValue{
				attribute.String("route", fmt.Sprintf("/api/%d", i%8)),
				attribute.String("method", "GET"),
			},
			Timestamp: time.Unix(1_700_000_000, int64(i)),
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchmarkHistogramResourceMetrics = toResourceMetrics("benchmark-service", metrics)
	}
}
