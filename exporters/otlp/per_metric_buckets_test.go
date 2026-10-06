package otlp

import (
	"context"
	"testing"
	"time"

	"github.com/convoy-road-trips-app/stats/models"
	"github.com/stretchr/testify/require"
)

func TestOTLPUsesPerMetricBuckets(t *testing.T) {
	// Given: per-name bounds for metric A (in milliseconds, the unit it is
	// recorded in), global bounds, and metrics B (global) and C (no global)
	exporter, received := wireExporter(t, &models.OTLPConfig{
		HistogramBuckets: []float64{1, 10},
		BucketsByName:    map[string][]float64{"a_latency_ms": {100, 200, 400}},
	})
	now := time.Now()

	// When
	require.NoError(t, exporter.Export(context.Background(), []*models.Metric{
		{Name: "a_latency_ms", Type: models.MetricTypeHistogram, Value: 150, Timestamp: now},
		{Name: "b_latency_ms", Type: models.MetricTypeHistogram, Value: 5, Timestamp: now},
	}))

	// Then: A uses its own bounds and counts; B falls back to the global bounds
	request := <-received
	a := wireMetric(t, request, "a_latency_ms").GetHistogram().DataPoints[0]
	require.Equal(t, []float64{100, 200, 400}, a.ExplicitBounds)
	require.Equal(t, []uint64{0, 1, 0, 0}, a.BucketCounts)
	b := wireMetric(t, request, "b_latency_ms").GetHistogram().DataPoints[0]
	require.Equal(t, []float64{1, 10}, b.ExplicitBounds)
	require.Equal(t, []uint64{0, 1, 0}, b.BucketCounts)
}

func TestOTLPPerMetricBuckets_fall_back_to_default_without_global(t *testing.T) {
	// Given
	exporter, received := wireExporter(t, &models.OTLPConfig{
		BucketsByName: map[string][]float64{"a": {1, 2}},
	})

	// When
	require.NoError(t, exporter.Export(context.Background(), []*models.Metric{
		{Name: "b", Type: models.MetricTypeHistogram, Value: 0.01, Timestamp: time.Now()},
	}))

	// Then
	b := wireMetric(t, <-received, "b").GetHistogram().DataPoints[0]
	require.Equal(t, models.DefaultHistogramBuckets(), b.ExplicitBounds)
}

func TestOTLPConfig_Validate_rejects_invalid_per_name_buckets(t *testing.T) {
	// Given
	config := models.OTLPConfig{BucketsByName: map[string][]float64{"a": {2, 1}}}

	// When
	err := config.Validate()

	// Then
	require.ErrorContains(t, err, `"a"`)
}

func TestOTLPUnprefixedBucketsMatchBySuffix(t *testing.T) {
	exporter, received := wireExporter(t, &models.OTLPConfig{
		BucketsByName: map[string][]float64{models.UnprefixedBucketsKey("request.duration"): {1, 2}},
	})

	require.NoError(t, exporter.Export(context.Background(), []*models.Metric{
		{Name: "myapp.request.duration", Type: models.MetricTypeHistogram, Value: 1.5, Timestamp: time.Now()},
	}))

	dp := wireMetric(t, <-received, "myapp.request.duration").GetHistogram().DataPoints[0]
	require.Equal(t, []float64{1, 2}, dp.ExplicitBounds)
}

func TestOTLPUnprefixedBucketsBeatExponential(t *testing.T) {
	exporter, received := wireExporter(t, &models.OTLPConfig{
		ExponentialHistogram: &models.OTLPExponentialHistogram{},
		BucketsByName:        map[string][]float64{models.UnprefixedBucketsKey("request.duration"): {1, 2}},
	})
	now := time.Now()

	require.NoError(t, exporter.Export(context.Background(), []*models.Metric{
		{Name: "myapp.request.duration", Type: models.MetricTypeHistogram, Value: 1.5, Timestamp: now},
		{Name: "myapp.other", Type: models.MetricTypeHistogram, Value: 5, Timestamp: now},
	}))

	request := <-received
	matched := wireMetric(t, request, "myapp.request.duration")
	require.Nil(t, matched.GetExponentialHistogram())
	require.Equal(t, []float64{1, 2}, matched.GetHistogram().GetDataPoints()[0].GetExplicitBounds())
	other := wireMetric(t, request, "myapp.other")
	require.Nil(t, other.GetHistogram())
	require.Len(t, other.GetExponentialHistogram().GetDataPoints(), 1)
}
