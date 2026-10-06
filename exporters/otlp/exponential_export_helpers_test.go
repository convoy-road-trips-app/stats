package otlp

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

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
