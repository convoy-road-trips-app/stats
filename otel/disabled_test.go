package otel

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric"
)

func TestMeterProvider_disabled_registers_no_observable_callbacks(t *testing.T) {
	// Given: OTEL_SDK_DISABLED=true and callbacks of every registration style
	t.Setenv("OTEL_SDK_DISABLED", "true")
	provider, err := NewMeterProvider()
	require.NoError(t, err)
	meter := provider.Meter("disabled")
	var calls atomic.Int64

	_, err = meter.Int64ObservableGauge("inline", metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
		calls.Add(1)
		o.Observe(1)
		return nil
	}))
	require.NoError(t, err)
	gauge, err := meter.Int64ObservableGauge("registered")
	require.NoError(t, err)
	registration, err := meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		calls.Add(1)
		return nil
	}, gauge)
	require.NoError(t, err)

	// When: the provider collects and shuts down
	ctx := context.Background()
	require.NoError(t, provider.ForceFlush(ctx))
	require.NoError(t, provider.Shutdown(ctx))

	// Then: nothing was registered or called, and unregistering is safe
	require.Zero(t, calls.Load())
	require.Empty(t, provider.observers.callbacks)
	require.False(t, provider.observers.started)
	require.NoError(t, registration.Unregister())
}

func TestMeterProvider_disabled_instruments_record_nothing(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "true")
	provider, err := NewMeterProvider()
	require.NoError(t, err)
	require.True(t, provider.client.Disabled())
	counter, err := provider.Meter("m").Int64Counter("c")
	require.NoError(t, err)
	counter.Add(context.Background(), 1)
	require.Zero(t, provider.client.Stats().Pipeline.Processed)
	require.NoError(t, provider.Shutdown(context.Background()))
}
