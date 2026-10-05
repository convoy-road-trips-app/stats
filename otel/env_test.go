package otel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/convoy-road-trips-app/stats"
)

// envClientOptions keeps env tests deterministic: one worker, export on flush.
func envClientOptions() MeterProviderOption {
	return WithStatsOptions(
		stats.WithServiceName("env-e2e"),
		stats.WithVersionReporting(false),
		stats.WithWorkers(1),
		stats.WithFlushInterval(time.Hour),
		stats.WithUDPTimeout(10*time.Second),
	)
}

func TestMeterProviderFromEnv(t *testing.T) {
	t.Run("environment endpoint reaches the collector", func(t *testing.T) {
		receiver := newMetricsReceiver(t)
		t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", receiver.server.URL)
		provider, err := NewMeterProviderFromEnv(envClientOptions())
		require.NoError(t, err)
		t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

		counter, err := provider.Meter("env").Int64Counter("env_requests_total")
		require.NoError(t, err)
		counter.Add(context.Background(), 3)
		require.NoError(t, provider.ForceFlush(context.Background()))

		require.InDelta(t, 3.0, receiver.await(t, "env_requests_total").value, 0)
	})

	t.Run("explicit option beats the environment", func(t *testing.T) {
		receiver := newMetricsReceiver(t)
		var envHits atomic.Int64
		decoy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			envHits.Add(1)
		}))
		t.Cleanup(decoy.Close)
		t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", decoy.URL)
		provider, err := NewMeterProviderFromEnv(envClientOptions(), WithStatsOptions(stats.WithOTLP(&stats.OTLPConfig{
			Endpoint: receiver.server.URL[len("http://"):],
			Insecure: true,
			Protocol: stats.OTLPProtocolHTTP,
		})))
		require.NoError(t, err)
		t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

		counter, err := provider.Meter("env").Int64Counter("explicit_total")
		require.NoError(t, err)
		counter.Add(context.Background(), 1)
		require.NoError(t, provider.ForceFlush(context.Background()))

		receiver.await(t, "explicit_total")
		require.Zero(t, envHits.Load(), "the environment endpoint must not receive exports")
	})
}

type clientReport struct {
	Hits int `metric:"hits" type:"counter"`
}

func TestMeterProviderClientReportsAndObserves(t *testing.T) {
	receiver := newMetricsReceiver(t)
	provider := receiver.provider(t)
	client := provider.Client()
	require.NotNil(t, client)
	ctx := context.Background()

	require.NoError(t, stats.Report(ctx, client.WithPrefix("svc"), clientReport{Hits: 2}))
	require.NoError(t, client.Observe(ctx, "op_duration", 1500*time.Millisecond))
	require.NoError(t, provider.ForceFlush(ctx))

	require.InDelta(t, 2.0, receiver.await(t, "svc.hits").value, 0)
	require.InDelta(t, 1.5, receiver.await(t, "op_duration").value, 1e-12)
}

func TestMeterProviderFromEnv_sdkDisabledIsNoOp(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
	provider, err := NewMeterProviderFromEnv()
	require.NoError(t, err)
	require.True(t, provider.Client().Disabled())
	ctx := context.Background()

	meter := provider.Meter("off")
	counter, err := meter.Int64Counter("c")
	require.NoError(t, err)
	histogram, err := meter.Float64Histogram("h")
	require.NoError(t, err)
	require.NotPanics(t, func() {
		counter.Add(ctx, 1)
		histogram.Record(ctx, 1)
		require.NoError(t, provider.Client().Observe(ctx, "o", time.Second))
	})
	require.Zero(t, provider.Client().Stats().Pipeline.Processed)
	require.NoError(t, provider.ForceFlush(ctx))
	require.NoError(t, provider.Shutdown(ctx))
}
