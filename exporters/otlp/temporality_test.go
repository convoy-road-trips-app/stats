package otlp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/convoy-road-trips-app/stats/models"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

type collectingExporter struct {
	collections []metricdata.ResourceMetrics
	failNext    error
}

func (c *collectingExporter) Export(_ context.Context, rm *metricdata.ResourceMetrics) error {
	if c.failNext != nil {
		err := c.failNext
		c.failNext = nil
		return err
	}
	c.collections = append(c.collections, *rm)
	return nil
}

func (c *collectingExporter) Shutdown(context.Context) error { return nil }

func TestExporter_cumulative_values_across_batches(t *testing.T) {
	// Given
	start := time.Unix(100, 0)
	first := []*models.Metric{
		{Name: "requests_total", Type: models.MetricTypeCounter, Value: 2, Timestamp: start},
		{Name: "requests_total", Type: models.MetricTypeCounter, Value: 3, Timestamp: start},
		{Name: "duration_seconds", Type: models.MetricTypeHistogram, Value: 0.01, Timestamp: start},
	}
	second := []*models.Metric{
		{Name: "requests_total", Type: models.MetricTypeCounter, Value: 4, Timestamp: start.Add(time.Second)},
		{Name: "duration_seconds", Type: models.MetricTypeHistogram, Value: 0.2, Timestamp: start.Add(time.Second)},
	}
	collector := &collectingExporter{}
	exporter := &Exporter{config: &models.OTLPConfig{Enabled: true}, otlpExporter: collector}

	// When
	require.NoError(t, exporter.Export(context.Background(), first))
	require.NoError(t, exporter.Export(context.Background(), second))

	// Then
	require.Len(t, collector.collections, 2)
	for i, want := range []float64{5, 9} {
		sum := metricByName(t, collector.collections[i], "requests_total").Data.(metricdata.Sum[float64])
		require.Equal(t, metricdata.CumulativeTemporality, sum.Temporality)
		require.Len(t, sum.DataPoints, 1)
		require.InDelta(t, want, sum.DataPoints[0].Value, 0.001)
		require.Equal(t, start, sum.DataPoints[0].StartTime)
	}
	for i, want := range []struct {
		count uint64
		sum   float64
	}{{1, 0.01}, {2, 0.21}} {
		hist := metricByName(t, collector.collections[i], "duration_seconds").Data.(metricdata.Histogram[float64])
		require.Equal(t, metricdata.CumulativeTemporality, hist.Temporality)
		require.Len(t, hist.DataPoints, 1)
		require.Equal(t, want.count, hist.DataPoints[0].Count)
		require.InDelta(t, want.sum, hist.DataPoints[0].Sum, 0.000001)
		require.Equal(t, start, hist.DataPoints[0].StartTime)
	}
}

func TestExporter_cumulative_values_include_failed_export_interval(t *testing.T) {
	// Given
	start := time.Unix(100, 0)
	collector := &collectingExporter{failNext: errors.New("receiver unavailable")}
	exporter := &Exporter{config: &models.OTLPConfig{Enabled: true}, otlpExporter: collector}

	// When
	err := exporter.Export(context.Background(), []*models.Metric{
		{Name: "requests_total", Type: models.MetricTypeCounter, Value: 2, Timestamp: start},
	})
	require.Error(t, err)
	require.NoError(t, exporter.Export(context.Background(), []*models.Metric{
		{Name: "requests_total", Type: models.MetricTypeCounter, Value: 3, Timestamp: start.Add(time.Second)},
	}))

	// Then
	require.Len(t, collector.collections, 1)
	sum := metricByName(t, collector.collections[0], "requests_total").Data.(metricdata.Sum[float64])
	require.InDelta(t, float64(5), sum.DataPoints[0].Value, 0.001)
}

func TestExporter_delta_values_across_batches(t *testing.T) {
	// Given
	start := time.Unix(100, 0)
	collector := &collectingExporter{}
	exporter := &Exporter{config: &models.OTLPConfig{Enabled: true, Temporality: models.Delta}, otlpExporter: collector}

	// When
	require.NoError(t, exporter.Export(context.Background(), []*models.Metric{
		{Name: "requests_total", Type: models.MetricTypeCounter, Value: 2, Timestamp: start},
	}))
	require.NoError(t, exporter.Export(context.Background(), []*models.Metric{
		{Name: "requests_total", Type: models.MetricTypeCounter, Value: 4, Timestamp: start.Add(time.Second)},
	}))

	// Then
	sum := metricByName(t, collector.collections[1], "requests_total").Data.(metricdata.Sum[float64])
	require.Equal(t, metricdata.DeltaTemporality, sum.Temporality)
	require.InDelta(t, float64(4), sum.DataPoints[0].Value, 0.001)
	require.Equal(t, start, sum.DataPoints[0].StartTime)
}

func TestExporter_delta_preserves_observations_within_each_batch(t *testing.T) {
	// Given
	start := time.Unix(100, 0)
	collector := &collectingExporter{}
	exporter := &Exporter{config: &models.OTLPConfig{Enabled: true, Temporality: models.Delta}, otlpExporter: collector}

	// When
	require.NoError(t, exporter.Export(context.Background(), []*models.Metric{
		{Name: "requests_total", Type: models.MetricTypeCounter, Value: 2, Timestamp: start},
		{Name: "requests_total", Type: models.MetricTypeCounter, Value: 3, Timestamp: start},
	}))

	// Then
	sum := metricByName(t, collector.collections[0], "requests_total").Data.(metricdata.Sum[float64])
	require.Equal(t, metricdata.DeltaTemporality, sum.Temporality)
	require.Len(t, sum.DataPoints, 1)
	require.InDelta(t, float64(5), sum.DataPoints[0].Value, 0.001)
}

func TestExporter_cumulative_separates_attribute_series(t *testing.T) {
	// Given
	start := time.Unix(100, 0)
	collector := &collectingExporter{}
	exporter := &Exporter{config: &models.OTLPConfig{Enabled: true}, otlpExporter: collector}
	series := func(route string, value float64) *models.Metric {
		return &models.Metric{
			Name: "requests_total", Type: models.MetricTypeCounter, Value: value,
			Timestamp: start, Attributes: []attribute.KeyValue{attribute.String("route", route)},
		}
	}

	// When
	require.NoError(t, exporter.Export(context.Background(), []*models.Metric{series("/a", 2), series("/b", 4)}))
	require.NoError(t, exporter.Export(context.Background(), []*models.Metric{series("/a", 3)}))

	// Then
	sum := metricByName(t, collector.collections[1], "requests_total").Data.(metricdata.Sum[float64])
	require.Len(t, sum.DataPoints, 1)
	require.InDelta(t, float64(5), sum.DataPoints[0].Value, 0.001)
}

func TestExporter_cumulative_point_time_advances_when_later_export_holds_older_observations(t *testing.T) {
	// Given: two workers whose batches of one series reach the exporter out of
	// observation order, within the same millisecond
	observed := time.Unix(100, 500_000)
	newer := []*models.Metric{
		{Name: "requests_total", Type: models.MetricTypeCounter, Value: 5, Timestamp: observed.Add(300 * time.Microsecond)},
		{Name: "duration_seconds", Type: models.MetricTypeHistogram, Value: 0.01, Timestamp: observed.Add(300 * time.Microsecond)},
	}
	older := []*models.Metric{
		{Name: "requests_total", Type: models.MetricTypeCounter, Value: 1, Timestamp: observed},
		{Name: "requests_total", Type: models.MetricTypeCounter, Value: 1, Timestamp: observed},
		{Name: "duration_seconds", Type: models.MetricTypeHistogram, Value: 0.02, Timestamp: observed},
	}
	collector := &collectingExporter{}
	exporter := &Exporter{config: &models.OTLPConfig{Enabled: true}, otlpExporter: collector}

	// When
	require.NoError(t, exporter.Export(context.Background(), newer))
	require.NoError(t, exporter.Export(context.Background(), older))

	// Then: the larger total lands one millisecond after the previous point,
	// however many observations of the series the late batch holds
	sums := [2]time.Time{}
	hists := [2]time.Time{}
	for i, rm := range collector.collections {
		sums[i] = metricByName(t, rm, "requests_total").Data.(metricdata.Sum[float64]).DataPoints[0].Time
		hists[i] = metricByName(t, rm, "duration_seconds").Data.(metricdata.Histogram[float64]).DataPoints[0].Time
	}
	require.Equal(t, sums[0].Add(time.Millisecond), sums[1])
	require.Equal(t, hists[0].Add(time.Millisecond), hists[1])
}

func metricByName(t *testing.T, rm metricdata.ResourceMetrics, name string) metricdata.Metrics {
	t.Helper()
	for _, m := range rm.ScopeMetrics[0].Metrics {
		if m.Name == name {
			return m
		}
	}
	t.Fatalf("metric %q missing", name)
	return metricdata.Metrics{}
}

func TestExporter_cumulative_histogram_keeps_extrema_of_earlier_exports(t *testing.T) {
	// Given: an earlier export saw 0.5 and 8, the next batch only 2
	start := time.Unix(100, 0)
	collector := &collectingExporter{}
	exporter := &Exporter{config: &models.OTLPConfig{Enabled: true}, otlpExporter: collector}
	require.NoError(t, exporter.Export(context.Background(), []*models.Metric{
		{Name: "duration_seconds", Type: models.MetricTypeHistogram, Value: 0.5, Timestamp: start},
		{Name: "duration_seconds", Type: models.MetricTypeHistogram, Value: 8, Timestamp: start},
	}))

	// When
	require.NoError(t, exporter.Export(context.Background(), []*models.Metric{
		{Name: "duration_seconds", Type: models.MetricTypeHistogram, Value: 2, Timestamp: start.Add(time.Second)},
	}))

	// Then
	point := metricByName(t, collector.collections[1], "duration_seconds").Data.(metricdata.Histogram[float64]).DataPoints[0]
	require.Equal(t, uint64(3), point.Count)
	require.Equal(t, metricdata.NewExtrema(0.5), point.Min)
	require.Equal(t, metricdata.NewExtrema(8.0), point.Max)
}

func TestExporter_delta_histogram_starts_where_the_previous_export_ended(t *testing.T) {
	// Given
	start := time.Unix(100, 0)
	collector := &collectingExporter{}
	exporter := &Exporter{config: &models.OTLPConfig{Enabled: true, Temporality: models.Delta}, otlpExporter: collector}
	require.NoError(t, exporter.Export(context.Background(), []*models.Metric{
		{Name: "duration_seconds", Type: models.MetricTypeHistogram, Value: 0.5, Timestamp: start},
	}))

	// When
	require.NoError(t, exporter.Export(context.Background(), []*models.Metric{
		{Name: "duration_seconds", Type: models.MetricTypeHistogram, Value: 2, Timestamp: start.Add(time.Second)},
	}))

	// Then
	point := metricByName(t, collector.collections[1], "duration_seconds").Data.(metricdata.Histogram[float64]).DataPoints[0]
	require.Equal(t, uint64(1), point.Count)
	require.Equal(t, start, point.StartTime)
	require.Equal(t, metricdata.NewExtrema(2.0), point.Min)
}
