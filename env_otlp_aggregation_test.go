package stats

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/convoy-road-trips-app/stats/models"
)

const envHistogramAggregation = "OTEL_EXPORTER_OTLP_METRICS_DEFAULT_HISTOGRAM_AGGREGATION"

func TestEnvHistogramAggregationBase2UsesExponentialDefaults(t *testing.T) {
	t.Setenv(envHistogramAggregation, "base2_exponential_bucket_histogram")

	cfg := newConfigFrom(t, WithOTLP(&OTLPConfig{Endpoint: "c:4317"}))

	require.NotNil(t, cfg.OTLP.ExponentialHistogram)
	require.Equal(t, models.OTLPExponentialHistogram{
		MaxSize: models.DefaultExponentialHistogramMaxSize, MaxScale: models.DefaultExponentialHistogramMaxScale,
	}, cfg.OTLP.ExponentialHistogram.Resolved())
}

func TestEnvHistogramAggregationExplicitBucketsKeepsExplicit(t *testing.T) {
	t.Setenv(envHistogramAggregation, "explicit_bucket_histogram")

	cfg := newConfigFrom(t, WithOTLPFromEnv(), WithOTLPExportTimeout(time.Second))

	require.Nil(t, cfg.OTLP.ExponentialHistogram)
}

func TestEnvHistogramAggregationIsCaseInsensitive(t *testing.T) {
	t.Setenv(envHistogramAggregation, " Base2_Exponential_Bucket_Histogram ")

	cfg := newConfigFrom(t, WithOTLP(&OTLPConfig{Endpoint: "c:4317"}))

	require.NotNil(t, cfg.OTLP.ExponentialHistogram)
}

func TestExponentialOptionBeatsHistogramAggregationEnv(t *testing.T) {
	t.Setenv(envHistogramAggregation, "base2_exponential_bucket_histogram")

	cfg := newConfigFrom(t, WithOTLP(&OTLPConfig{Endpoint: "c:4317"}), WithExponentialHistogram(40, 5))

	require.Equal(t, models.OTLPExponentialHistogram{MaxSize: 40, MaxScale: 5}, *cfg.OTLP.ExponentialHistogram)
}

func TestExplicitBucketsOptionBeatsHistogramAggregationEnv(t *testing.T) {
	t.Setenv(envHistogramAggregation, "base2_exponential_bucket_histogram")

	cfg := newConfigFrom(t, WithOTLP(&OTLPConfig{Endpoint: "c:4317"}), WithHistogramBuckets([]float64{1, 2, 3}))

	require.Nil(t, cfg.OTLP.ExponentialHistogram, "stated buckets must not be silently dropped")
}

func TestHistogramAggregationEnvOnlyReadsMetricsVariable(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_DEFAULT_HISTOGRAM_AGGREGATION", "base2_exponential_bucket_histogram")

	cfg := newConfigFrom(t, WithOTLP(&OTLPConfig{Endpoint: "c:4317"}))

	require.Nil(t, cfg.OTLP.ExponentialHistogram)
}

func TestInvalidHistogramAggregationEnvFailsNewClient(t *testing.T) {
	t.Setenv(envHistogramAggregation, "drop")

	_, err := NewClient(WithOTLPFromEnv(), WithOTLP(&OTLPConfig{Endpoint: "c:4317"}))

	require.ErrorIs(t, err, ErrInvalidConfig)
	require.ErrorContains(t, err, envHistogramAggregation)
}

func TestEnvTemporalityLowMemoryExportsDelta(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "LowMemory")

	cfg := newConfigFrom(t, WithOTLPFromEnv())

	require.Equal(t, Delta, cfg.OTLP.Temporality)
}

func TestEnvMetricExportTimeoutIsFallbackForExportTimeout(t *testing.T) {
	t.Setenv("OTEL_METRIC_EXPORT_TIMEOUT", "4000")
	require.Equal(t, 4*time.Second, newConfigFrom(t, WithOTLPFromEnv()).OTLP.ExportTimeout)

	t.Setenv("OTEL_EXPORTER_OTLP_TIMEOUT", "2000")
	require.Equal(t, 2*time.Second, newConfigFrom(t, WithOTLPFromEnv()).OTLP.ExportTimeout, "the transport variable wins")

	require.Equal(t, time.Second, newConfigFrom(t, WithOTLPFromEnv(), WithOTLPExportTimeout(time.Second)).OTLP.ExportTimeout)
}

func TestInvalidMetricExportTimeoutNamesVariable(t *testing.T) {
	t.Setenv("OTEL_METRIC_EXPORT_TIMEOUT", "soon")

	_, err := NewClient(WithOTLPFromEnv())

	require.ErrorContains(t, err, "OTEL_METRIC_EXPORT_TIMEOUT")
}
