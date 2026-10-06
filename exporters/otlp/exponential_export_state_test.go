package otlp

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/convoy-road-trips-app/stats/models"
)

func TestExpoDeltaSuccessiveExports(t *testing.T) {
	// Given
	start := time.Unix(100, 0)
	exporter, collector := expoExporter(models.Delta, 160)
	intervals := [][]float64{{1, 2}, {4}, {8, -16}}

	// When: one export per interval, a second apart
	for i, values := range intervals {
		at := start.Add(time.Duration(i) * time.Second)
		require.NoError(t, exporter.Export(context.Background(), latencies(at, values...)))
	}

	// Then: each point holds its own interval and starts where the previous ended
	require.Len(t, collector.collections, len(intervals))
	for i, values := range intervals {
		histogram := latencyHistogram(t, collector.collections[i])
		require.Equal(t, metricdata.DeltaTemporality, histogram.Temporality)
		require.Len(t, histogram.DataPoints, 1)
		point := &histogram.DataPoints[0]
		requireExpoContent(t, recorded(160, 20, values...), point)
		require.Equal(t, start.Add(time.Duration(max(i-1, 0))*time.Second), point.StartTime, "export %d", i)
		require.Equal(t, start.Add(time.Duration(i)*time.Second), point.Time, "export %d", i)
	}
}

func TestExpoCumulativeKeepsStateAfterFailedSend(t *testing.T) {
	// Given: a first export that the receiver rejects
	start := time.Unix(100, 0)
	exporter, collector := expoExporter(models.Cumulative, 160)
	collector.failNext = errors.New("receiver unavailable")
	require.Error(t, exporter.Export(context.Background(), latencies(start, 1, 2)))

	// When
	require.NoError(t, exporter.Export(context.Background(), latencies(start.Add(time.Second), 4)))

	// Then: the next export includes the interval the receiver did not get
	require.Len(t, collector.collections, 1)
	point := latencyPoint(t, collector.collections[0])
	requireExpoContent(t, recorded(160, 20, 1, 2, 4), &point)
	require.Equal(t, start, point.StartTime)
}

func TestExpoCumulativeMergesDisjointRanges(t *testing.T) {
	// Given: small values in one export, and far larger ones in the next,
	// with at most 8 buckets per range
	start := time.Unix(100, 0)
	exporter, collector := expoExporter(models.Cumulative, 8)
	small := []float64{0.001, 0.002, -0.003}
	large := []float64{1000, 2000, -5000}
	require.NoError(t, exporter.Export(context.Background(), latencies(start, small...)))

	// When
	require.NoError(t, exporter.Export(context.Background(), latencies(start.Add(time.Second), large...)))

	// Then: the merge downscales until both ranges fit 8 buckets again, as
	// recording every value into one histogram does
	first := latencyPoint(t, collector.collections[0])
	merged := latencyPoint(t, collector.collections[1])
	requireExpoContent(t, recorded(8, 20, slices.Concat(small, large)...), &merged)
	require.Less(t, merged.Scale, first.Scale)
	require.LessOrEqual(t, len(merged.PositiveBucket.Counts), 8)
	require.LessOrEqual(t, len(merged.NegativeBucket.Counts), 8)
}

func TestExpoCumulativeMergesExtremeValues(t *testing.T) {
	// Given: 1e-300 in one export and 1e300 in the next, of both signs
	start := time.Unix(100, 0)
	exporter, collector := expoExporter(models.Cumulative, 160)
	require.NoError(t, exporter.Export(context.Background(), latencies(start, 1e-300, -1e-300)))

	// When
	require.NoError(t, exporter.Export(context.Background(), latencies(start.Add(time.Second), 1e300, -1e300)))

	// Then: all four values stay counted within 160 buckets per range
	point := latencyPoint(t, collector.collections[1])
	requireExpoContent(t, recorded(160, 20, 1e-300, -1e-300, 1e300, -1e300), &point)
	require.Equal(t, metricdata.NewExtrema(-1e300), point.Min)
	require.Equal(t, metricdata.NewExtrema(1e300), point.Max)
	require.LessOrEqual(t, len(point.PositiveBucket.Counts), 160)
	require.LessOrEqual(t, len(point.NegativeBucket.Counts), 160)
}

func TestExpoCumulativeKeepsExtremaOfEarlierExports(t *testing.T) {
	// Given: an earlier export saw 0.5 and 8, the next sees only 2
	start := time.Unix(100, 0)
	exporter, collector := expoExporter(models.Cumulative, 160)
	require.NoError(t, exporter.Export(context.Background(), latencies(start, 0.5, 8)))

	// When
	require.NoError(t, exporter.Export(context.Background(), latencies(start.Add(time.Second), 2)))

	// Then
	point := latencyPoint(t, collector.collections[1])
	require.Equal(t, uint64(3), point.Count)
	require.Equal(t, metricdata.NewExtrema(0.5), point.Min)
	require.Equal(t, metricdata.NewExtrema(8.0), point.Max)
}

func TestExpoExemplarsBelongToTheirExport(t *testing.T) {
	// Given: a first export with a sampled observation
	start := time.Unix(100, 0)
	exporter, collector := expoExporter(models.Cumulative, 160)
	require.NoError(t, exporter.Export(context.Background(), []*models.Metric{
		sampled(&models.Metric{Name: "latency", Type: models.MetricTypeHistogram, Value: 0.2, Timestamp: start}, traceA, spanA),
	}))

	// When: a later export and an idle one have no sampled observation
	require.NoError(t, exporter.Export(context.Background(), latencies(start.Add(time.Second), 0.3)))
	require.NoError(t, exporter.ExportIdle(context.Background()))

	// Then
	require.Equal(t, []metricdata.Exemplar[float64]{{Time: start, Value: 0.2, TraceID: traceA[:], SpanID: spanA[:]}},
		latencyPoint(t, collector.collections[0]).Exemplars)
	require.Empty(t, latencyPoint(t, collector.collections[1]).Exemplars)
	require.Empty(t, latencyPoint(t, collector.collections[2]).Exemplars)
}

func TestExpoExemplarsKeepTheNewestUpToTheReservoirSize(t *testing.T) {
	// Given: max size 2, so a point keeps min(20, 2) exemplars, and three
	// sampled observations whose oldest is in the middle of the batch
	start := time.Unix(100, 0)
	exporter, collector := expoExporter(models.Delta, 2)
	batch := []*models.Metric{
		sampled(&models.Metric{Name: "latency", Type: models.MetricTypeHistogram, Value: 1, Timestamp: start.Add(time.Second)}, traceA, spanA),
		sampled(&models.Metric{Name: "latency", Type: models.MetricTypeHistogram, Value: 2, Timestamp: start}, traceB, spanB),
		sampled(&models.Metric{Name: "latency", Type: models.MetricTypeHistogram, Value: 3, Timestamp: start.Add(2 * time.Second)}, traceA, spanB),
	}

	// When
	require.NoError(t, exporter.Export(context.Background(), batch))

	// Then: the two newest, oldest first
	require.Equal(t, []metricdata.Exemplar[float64]{
		{Time: start.Add(time.Second), Value: 1, TraceID: traceA[:], SpanID: spanA[:]},
		{Time: start.Add(2 * time.Second), Value: 3, TraceID: traceA[:], SpanID: spanB[:]},
	}, latencyPoint(t, collector.collections[0]).Exemplars)
}
