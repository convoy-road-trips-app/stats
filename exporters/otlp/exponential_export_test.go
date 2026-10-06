package otlp

import (
	"context"
	"errors"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"

	"github.com/convoy-road-trips-app/stats/models"
)

// expoExporter returns an exporter whose histograms are exponential, with at
// most maxSize buckets per range from scale 20, and the collector it sends to.
func expoExporter(temporality models.Temporality, maxSize int32) (*Exporter, *collectingExporter) {
	collector := &collectingExporter{}
	config := &models.OTLPConfig{
		Enabled:              true,
		Temporality:          temporality,
		ExponentialHistogram: &models.OTLPExponentialHistogram{MaxSize: maxSize, MaxScale: 20},
	}
	return &Exporter{config: config, otlpExporter: collector}, collector
}

// latencies returns one observation of the histogram "latency" per value, all
// at time at.
func latencies(at time.Time, values ...float64) []*models.Metric {
	metrics := make([]*models.Metric, 0, len(values))
	for _, value := range values {
		metrics = append(metrics, &models.Metric{Name: "latency", Type: models.MetricTypeHistogram, Value: value, Timestamp: at})
	}
	return metrics
}

// latencyHistogram returns the histogram "latency" of rm, which must be
// exponential.
func latencyHistogram(t *testing.T, rm metricdata.ResourceMetrics) metricdata.ExponentialHistogram[float64] {
	t.Helper()
	data := metricByName(t, rm, "latency").Data
	histogram, ok := data.(metricdata.ExponentialHistogram[float64])
	require.True(t, ok, "latency is a %T, not an exponential histogram", data)
	return histogram
}

// latencyPoint returns the only datapoint of the exponential histogram
// "latency" of rm.
func latencyPoint(t *testing.T, rm metricdata.ResourceMetrics) metricdata.ExponentialHistogramDataPoint[float64] {
	t.Helper()
	points := latencyHistogram(t, rm).DataPoints
	require.Len(t, points, 1)
	return points[0]
}

// requireExpoContent compares the scale, buckets, counts and extrema of got
// with those of want, and the sums with a tolerance for the order of float
// additions. Attributes, timestamps and exemplars are not compared.
func requireExpoContent(t *testing.T, want *expoHistogram, got *metricdata.ExponentialHistogramDataPoint[float64]) {
	t.Helper()
	w := want.snapshot()
	require.InDelta(t, w.Sum, got.Sum, 1e-9*math.Abs(w.Sum)+1e-12)
	content := *got
	content.Attributes, content.StartTime, content.Time = w.Attributes, w.StartTime, w.Time
	content.Exemplars, content.Sum = w.Exemplars, w.Sum
	require.Equal(t, w, content)
}

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
