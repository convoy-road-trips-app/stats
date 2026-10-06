package otlp

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"

	"github.com/convoy-road-trips-app/stats/models"
)

func TestOTLPWireExponential(t *testing.T) {
	// Given: exponential histograms of at most 4 buckets per range, sent to a
	// real OTLP/HTTP receiver
	exporter, received := wireExporter(t, &models.OTLPConfig{
		ExponentialHistogram: &models.OTLPExponentialHistogram{MaxSize: 4, MaxScale: 20},
	})

	// When
	require.NoError(t, exporter.Export(context.Background(), latencies(time.Now(), 1, 2, 4, 0, -1)))

	// Then: 1, 2 and 4 fit 4 buckets from scale 0, where powers of two are bounds
	metric := wireMetric(t, <-received, "latency")
	require.Nil(t, metric.GetHistogram())
	histogram := metric.GetExponentialHistogram()
	require.Equal(t, metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE, histogram.GetAggregationTemporality())
	require.Len(t, histogram.GetDataPoints(), 1)
	point := histogram.GetDataPoints()[0]
	require.Equal(t, int32(0), point.GetScale())
	require.Equal(t, uint64(5), point.GetCount())
	require.Equal(t, uint64(1), point.GetZeroCount())
	require.Equal(t, int32(-1), point.GetPositive().GetOffset())
	require.Equal(t, []uint64{1, 1, 1}, point.GetPositive().GetBucketCounts())
	require.Equal(t, int32(-1), point.GetNegative().GetOffset())
	require.Equal(t, []uint64{1}, point.GetNegative().GetBucketCounts())
	require.InDelta(t, 6.0, point.GetSum(), 1e-9)
	require.InDelta(t, -1.0, point.GetMin(), 1e-9)
	require.InDelta(t, 4.0, point.GetMax(), 1e-9)
}

func TestExpoExplicitBucketsForANameStayExplicit(t *testing.T) {
	// Given: exponential histograms, global bounds, and explicit bounds for one name
	exporter, received := wireExporter(t, &models.OTLPConfig{
		ExponentialHistogram: &models.OTLPExponentialHistogram{},
		HistogramBuckets:     []float64{1, 10},
		BucketsByName:        map[string][]float64{"explicit_ms": {100, 200}},
	})
	now := time.Now()

	// When
	require.NoError(t, exporter.Export(context.Background(), []*models.Metric{
		{Name: "explicit_ms", Type: models.MetricTypeHistogram, Value: 150, Timestamp: now},
		{Name: "latency", Type: models.MetricTypeHistogram, Value: 5, Timestamp: now},
	}))

	// Then: the named metric keeps its own bounds; the global bounds give way
	request := <-received
	explicit := wireMetric(t, request, "explicit_ms")
	require.Nil(t, explicit.GetExponentialHistogram())
	require.Len(t, explicit.GetHistogram().GetDataPoints(), 1)
	require.Equal(t, []float64{100, 200}, explicit.GetHistogram().GetDataPoints()[0].GetExplicitBounds())
	require.Equal(t, []uint64{0, 1, 0}, explicit.GetHistogram().GetDataPoints()[0].GetBucketCounts())
	latency := wireMetric(t, request, "latency")
	require.Nil(t, latency.GetHistogram())
	require.Len(t, latency.GetExponentialHistogram().GetDataPoints(), 1)
	require.Equal(t, uint64(1), latency.GetExponentialHistogram().GetDataPoints()[0].GetCount())
}

func TestExpoCumulativeReexport(t *testing.T) {
	// Given: an exponential histogram series observed once
	start := time.Unix(100, 0)
	exporter, collector := expoExporter(models.Cumulative, 160)
	require.NoError(t, exporter.Export(context.Background(), latencies(start, 0.5, 3, -2, 0)))

	// When: two export intervals pass without observations
	require.NoError(t, exporter.ExportIdle(context.Background()))
	require.NoError(t, exporter.ExportIdle(context.Background()))

	// Then: every export repeats the cumulative exponential point from the same start
	require.Len(t, collector.collections, 3)
	want := recorded(160, 20, 0.5, 3, -2, 0)
	var previous time.Time
	for i, rm := range collector.collections {
		histogram := latencyHistogram(t, rm)
		require.Equal(t, metricdata.CumulativeTemporality, histogram.Temporality, "export %d", i)
		require.Len(t, histogram.DataPoints, 1, "export %d", i)
		point := &histogram.DataPoints[0]
		requireExpoContent(t, want, point)
		require.Equal(t, start, point.StartTime, "export %d", i)
		require.True(t, point.Time.After(previous), "export %d: the point time must advance", i)
		previous = point.Time
	}
}

func TestExpoCumulativeReexportJoinsTheObservedHistogram(t *testing.T) {
	// Given: two series of one histogram, then an export observing only /a
	start := time.Unix(100, 0)
	exporter, collector := expoExporter(models.Cumulative, 160)
	observe := func(route string, value float64) *models.Metric {
		return &models.Metric{
			Name: "latency", Type: models.MetricTypeHistogram, Value: value, Timestamp: start,
			Attributes: []attribute.KeyValue{attribute.String("route", route)},
		}
	}
	require.NoError(t, exporter.Export(context.Background(), []*models.Metric{observe("/a", 1), observe("/b", 2)}))

	// When
	require.NoError(t, exporter.Export(context.Background(), []*models.Metric{observe("/a", 4)}))

	// Then: the repeated /b point joins the exponential histogram of /a
	metrics := collector.collections[1].ScopeMetrics[0].Metrics
	require.Len(t, metrics, 1)
	counts := map[string]uint64{}
	points := latencyHistogram(t, collector.collections[1]).DataPoints
	for i := range points {
		value, _ := points[i].Attributes.Value("route")
		counts[value.AsString()] = points[i].Count
	}
	require.Equal(t, map[string]uint64{"/a": 2, "/b": 1}, counts)
}

func TestExpoCumulativeReexportKeepsEachHistogramType(t *testing.T) {
	// Given: an exponential and an explicit-bucket histogram observed once
	start := time.Unix(100, 0)
	exporter, collector := expoExporter(models.Cumulative, 160)
	exporter.config.BucketsByName = map[string][]float64{"explicit_ms": {100}}
	require.NoError(t, exporter.Export(context.Background(), []*models.Metric{
		{Name: "explicit_ms", Type: models.MetricTypeHistogram, Value: 150, Timestamp: start},
		{Name: "latency", Type: models.MetricTypeHistogram, Value: 0.5, Timestamp: start},
	}))

	// When
	require.NoError(t, exporter.ExportIdle(context.Background()))

	// Then
	idle := collector.collections[1]
	require.Len(t, idle.ScopeMetrics[0].Metrics, 2)
	require.Equal(t, uint64(1), histogramPoint(t, idle, "explicit_ms").Count)
	require.Equal(t, uint64(1), latencyPoint(t, idle).Count)
}

func TestExpoCumulativeStateSharesNoBucketsWithExports(t *testing.T) {
	// Given: a cumulative point and its idle repeat, both handed to the transport
	start := time.Unix(100, 0)
	exporter, collector := expoExporter(models.Cumulative, 160)
	require.NoError(t, exporter.Export(context.Background(), latencies(start, 1, -1)))
	require.NoError(t, exporter.ExportIdle(context.Background()))

	// When: the transport overwrites the bucket counts it was given
	for _, rm := range collector.collections {
		point := latencyPoint(t, rm)
		point.PositiveBucket.Counts[0] = 99
		point.NegativeBucket.Counts[0] = 99
	}
	require.NoError(t, exporter.Export(context.Background(), latencies(start.Add(time.Second), 1)))

	// Then: the cumulative state kept its own counts
	point := latencyPoint(t, collector.collections[2])
	requireExpoContent(t, recorded(160, 20, 1, -1, 1), &point)
}
