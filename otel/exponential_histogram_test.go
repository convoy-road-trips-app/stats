package otel

import (
	"context"
	"testing"

	"github.com/convoy-road-trips-app/stats"
	"github.com/stretchr/testify/require"
)

func TestMeterProvider_WithExponentialHistogram_exports_exponential_histograms(t *testing.T) {
	// Given: OTel mode with exponential histograms at their defaults
	receiver := newMetricsReceiver(t)
	provider := receiver.provider(t, WithStatsOptions(stats.WithExponentialHistogram(0, 0)))
	histogram, err := provider.Meter("test").Float64Histogram("latency")
	require.NoError(t, err)
	histogram.Record(context.Background(), 0.25)
	histogram.Record(context.Background(), 4)

	// When
	require.NoError(t, provider.ForceFlush(context.Background()))

	// Then
	point := receiver.await(t, "latency")
	require.Equal(t, "exponential", point.kind)
	require.InDelta(t, 4.25, point.value, 1e-9)
}
