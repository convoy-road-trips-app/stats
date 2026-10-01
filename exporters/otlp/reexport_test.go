package otlp

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/convoy-road-trips-app/stats/models"
)

func observedOnce(start time.Time) []*models.Metric {
	attrs := []attribute.KeyValue{attribute.String("k", "v")}
	return []*models.Metric{
		{Name: "sync_total", Type: models.MetricTypeCounter, Value: 3, Timestamp: start, Attributes: attrs},
		{Name: "sync_seconds", Type: models.MetricTypeHistogram, Value: 0.2, Timestamp: start, Attributes: attrs},
		{Name: "sync_last", Type: models.MetricTypeGauge, Value: 7, Timestamp: start, Attributes: attrs},
	}
}

func TestExporter_ExportIdle_cumulative_repeats_every_series_with_same_values_and_start(t *testing.T) {
	// Given: one counter, histogram and gauge observed once
	start := time.Unix(100, 0)
	collector := &collectingExporter{}
	exporter := &Exporter{config: &models.OTLPConfig{Enabled: true}, otlpExporter: collector}
	require.NoError(t, exporter.Export(context.Background(), observedOnce(start)))

	// When: two export intervals pass without observations
	require.NoError(t, exporter.ExportIdle(context.Background()))
	require.NoError(t, exporter.ExportIdle(context.Background()))

	// Then
	require.Len(t, collector.collections, 3)
	for i, rm := range collector.collections {
		sum := metricByName(t, rm, "sync_total").Data.(metricdata.Sum[float64])
		require.Len(t, sum.DataPoints, 1, "export %d", i)
		require.InDelta(t, 3, sum.DataPoints[0].Value, 0.001)
		require.Equal(t, start, sum.DataPoints[0].StartTime)

		hist := metricByName(t, rm, "sync_seconds").Data.(metricdata.Histogram[float64])
		require.Len(t, hist.DataPoints, 1, "export %d", i)
		require.Equal(t, uint64(1), hist.DataPoints[0].Count)
		require.InDelta(t, 0.2, hist.DataPoints[0].Sum, 0.000001)
		require.Equal(t, start, hist.DataPoints[0].StartTime)

		gauge := metricByName(t, rm, "sync_last").Data.(metricdata.Gauge[float64])
		require.Len(t, gauge.DataPoints, 1, "export %d", i)
		require.InDelta(t, 7, gauge.DataPoints[0].Value, 0.001)
	}
	for _, name := range []string{"sync_total", "sync_seconds", "sync_last"} {
		require.True(t, pointTime(t, collector.collections[2], name).After(pointTime(t, collector.collections[1], name)),
			"%s: repeated point time must advance", name)
		require.True(t, pointTime(t, collector.collections[1], name).After(pointTime(t, collector.collections[0], name)))
	}
}

func pointTime(t *testing.T, rm metricdata.ResourceMetrics, name string) time.Time {
	t.Helper()
	switch data := metricByName(t, rm, name).Data.(type) {
	case metricdata.Sum[float64]:
		return data.DataPoints[0].Time
	case metricdata.Gauge[float64]:
		return data.DataPoints[0].Time
	case metricdata.Histogram[float64]:
		return data.DataPoints[0].Time
	}
	t.Fatalf("unexpected data for %s", name)
	return time.Time{}
}

func TestExporter_ExportIdle_delta_sends_nothing(t *testing.T) {
	// Given
	collector := &collectingExporter{}
	exporter := &Exporter{config: &models.OTLPConfig{Enabled: true, Temporality: models.Delta}, otlpExporter: collector}
	require.NoError(t, exporter.Export(context.Background(), observedOnce(time.Unix(100, 0))))

	// When
	require.NoError(t, exporter.ExportIdle(context.Background()))

	// Then
	require.Len(t, collector.collections, 1)
}

func TestExporter_delta_export_contains_only_observed_series(t *testing.T) {
	// Given
	collector := &collectingExporter{}
	exporter := &Exporter{config: &models.OTLPConfig{Enabled: true, Temporality: models.Delta}, otlpExporter: collector}
	start := time.Unix(100, 0)
	require.NoError(t, exporter.Export(context.Background(), observedOnce(start)))

	// When
	require.NoError(t, exporter.Export(context.Background(), []*models.Metric{
		{Name: "sync_total", Type: models.MetricTypeCounter, Value: 1, Timestamp: start.Add(time.Second)},
	}))

	// Then
	require.Len(t, collector.collections[1].ScopeMetrics[0].Metrics, 1)
}

func TestExporter_ExportIdle_before_any_observation_sends_nothing(t *testing.T) {
	// Given
	collector := &collectingExporter{}
	exporter := &Exporter{config: &models.OTLPConfig{Enabled: true}, otlpExporter: collector}

	// When
	require.NoError(t, exporter.ExportIdle(context.Background()))

	// Then
	require.Empty(t, collector.collections)
}
