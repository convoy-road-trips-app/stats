package otel

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/convoy-road-trips-app/stats"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
)

var primary = metric.WithAttributes(attribute.String("pool", "primary"))

func TestObservableInstruments_export_callback_values_with_labels_on_every_ForceFlush(t *testing.T) {
	// Given: all six observable instruments whose callbacks report the current round's value
	receiver := newMetricsReceiver(t)
	provider := receiver.provider(t)
	meter := provider.Meter("observable")
	var round atomic.Int64
	values := [][6]float64{{10, 1.5, -3, 2.5, 42, 0.75}, {25, 4, 5, -1.25, 40, 0.5}}
	current := func(i int) float64 { return values[round.Load()][i] }
	_, err := meter.Int64ObservableCounter("conns_opened_total", metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
		o.Observe(int64(current(0)), primary)
		return nil
	}))
	require.NoError(t, err)
	_, err = meter.Float64ObservableCounter("cpu_seconds_total", metric.WithFloat64Callback(func(_ context.Context, o metric.Float64Observer) error {
		o.Observe(current(1), primary)
		return nil
	}))
	require.NoError(t, err)
	_, err = meter.Int64ObservableUpDownCounter("queue_delta", metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
		o.Observe(int64(current(2)), primary)
		return nil
	}))
	require.NoError(t, err)
	_, err = meter.Float64ObservableUpDownCounter("balance", metric.WithFloat64Callback(func(_ context.Context, o metric.Float64Observer) error {
		o.Observe(current(3), primary)
		return nil
	}))
	require.NoError(t, err)
	_, err = meter.Int64ObservableGauge("conns_open", metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
		o.Observe(int64(current(4)), primary)
		return nil
	}))
	require.NoError(t, err)
	_, err = meter.Float64ObservableGauge("pool_utilization", metric.WithFloat64Callback(func(_ context.Context, o metric.Float64Observer) error {
		o.Observe(current(5), primary)
		return nil
	}))
	require.NoError(t, err)
	names := []string{"conns_opened_total", "cpu_seconds_total", "queue_delta", "balance", "conns_open", "pool_utilization"}
	kinds := []string{"sum", "sum", "gauge", "gauge", "gauge", "gauge"}

	for r := range values {
		round.Store(int64(r))

		// When
		require.NoError(t, provider.ForceFlush(context.Background()))

		// Then: counters export the observed cumulative value, the others the observed value
		for i, name := range names {
			point, ok := receiver.take(name + "{pool=primary}")
			require.True(t, ok, "round %d: %s missing", r, name)
			require.Equal(t, kinds[i], point.kind, name)
			require.Equal(t, i < 2, point.monotonic, name)
			require.InDelta(t, values[r][i], point.value, 1e-9, "round %d: %s", r, name)
		}
	}
}

func TestMeter_RegisterCallback_observes_several_instruments_until_Unregister(t *testing.T) {
	// Given
	receiver := newMetricsReceiver(t)
	provider := receiver.provider(t)
	meter := provider.Meter("multi")
	gauge, err := meter.Float64ObservableGauge("temperature")
	require.NoError(t, err)
	counter, err := meter.Int64ObservableCounter("bytes_total")
	require.NoError(t, err)
	var calls atomic.Int64
	registration, err := meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		calls.Add(1)
		o.ObserveFloat64(gauge, 21.5, metric.WithAttributes(attribute.String("pool", "east")))
		o.ObserveInt64(counter, 512, metric.WithAttributes(attribute.String("pool", "east")))
		return nil
	}, gauge, counter)
	require.NoError(t, err)
	require.NoError(t, provider.ForceFlush(context.Background()))
	require.Equal(t, 21.5, receiver.await(t, "temperature{pool=east}").value)
	require.Equal(t, float64(512), receiver.await(t, "bytes_total{pool=east}").value)

	// When
	require.NoError(t, registration.Unregister())
	require.NoError(t, provider.ForceFlush(context.Background()))

	// Then: the callback ran once, and a second Unregister is a no-op
	require.Equal(t, int64(1), calls.Load())
	_, ok := receiver.take("temperature{pool=east}")
	require.False(t, ok)
	require.NoError(t, registration.Unregister())
}

func TestMeter_RegisterCallback_validates_its_arguments(t *testing.T) {
	provider := newMetricsReceiver(t).provider(t)
	meter := provider.Meter("validation")
	gauge, err := meter.Int64ObservableGauge("depth")
	require.NoError(t, err)
	foreign, err := noop.NewMeterProvider().Meter("noop").Int64ObservableGauge("depth")
	require.NoError(t, err)
	otherMeter, err := provider.Meter("other").Int64ObservableGauge("depth")
	require.NoError(t, err)
	callback := func(context.Context, metric.Observer) error { return nil }

	t.Run("nil callback is rejected", func(t *testing.T) {
		registration, err := meter.RegisterCallback(nil, gauge)
		require.ErrorIs(t, err, ErrNilCallback)
		require.Nil(t, registration)
	})
	t.Run("instrument from another implementation is rejected", func(t *testing.T) {
		registration, err := meter.RegisterCallback(callback, foreign)
		require.ErrorIs(t, err, ErrForeignObservable)
		require.Nil(t, registration)
	})
	t.Run("instrument from another meter is skipped with an error", func(t *testing.T) {
		registration, err := meter.RegisterCallback(callback, otherMeter)
		require.ErrorIs(t, err, ErrObservableMeter)
		require.NotNil(t, registration)
		require.NoError(t, registration.Unregister())
	})
	t.Run("no instruments gives a no-op registration", func(t *testing.T) {
		registration, err := meter.RegisterCallback(callback)
		require.NoError(t, err)
		require.NoError(t, registration.Unregister())
	})
}

func TestMeterProvider_ForceFlush_returns_callback_errors_and_still_exports_other_callbacks(t *testing.T) {
	// Given: one failing callback, one healthy one, and one observing an unregistered instrument
	receiver := newMetricsReceiver(t)
	provider := receiver.provider(t)
	meter := provider.Meter("errors")
	errBackend := errors.New("backend unavailable")
	_, err := meter.Int64ObservableGauge("broken", metric.WithInt64Callback(func(context.Context, metric.Int64Observer) error {
		return errBackend
	}))
	require.NoError(t, err)
	healthy, err := meter.Int64ObservableGauge("healthy")
	require.NoError(t, err)
	unregistered, err := meter.Int64ObservableGauge("unregistered")
	require.NoError(t, err)
	_, err = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		o.ObserveInt64(healthy, 7, primary)
		o.ObserveInt64(unregistered, 9, primary)
		return nil
	}, healthy)
	require.NoError(t, err)

	// When
	err = provider.ForceFlush(context.Background())

	// Then
	require.ErrorIs(t, err, errBackend)
	require.ErrorIs(t, err, ErrUnregisteredObservable)
	require.Equal(t, float64(7), receiver.await(t, "healthy{pool=primary}").value)
	_, ok := receiver.take("unregistered{pool=primary}")
	require.False(t, ok)
}

func TestMeterProvider_ForceFlush_with_done_context_skips_callbacks(t *testing.T) {
	// Given
	provider := newMetricsReceiver(t).provider(t)
	var calls atomic.Int64
	_, err := provider.Meter("cancel").Int64ObservableGauge("depth", metric.WithInt64Callback(func(context.Context, metric.Int64Observer) error {
		calls.Add(1)
		return nil
	}))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// When
	err = provider.ForceFlush(ctx)

	// Then
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, calls.Load())
}

func TestMeterProvider_collects_observables_periodically_without_ForceFlush(t *testing.T) {
	// Given: periodic collection and a short pipeline flush interval
	receiver := newMetricsReceiver(t)
	provider := receiver.provider(
		t,
		WithCollectionInterval(10*time.Millisecond),
		WithStatsOptions(stats.WithFlushInterval(10*time.Millisecond)),
	)

	// When
	_, err := provider.Meter("periodic").Float64ObservableGauge("load", metric.WithFloat64Callback(func(_ context.Context, o metric.Float64Observer) error {
		o.Observe(0.25, primary)
		return nil
	}))
	require.NoError(t, err)

	// Then
	require.Equal(t, 0.25, receiver.await(t, "load{pool=primary}").value)
}

func TestMeterProvider_Shutdown_collects_once_more_then_stops_calling_callbacks(t *testing.T) {
	// Given
	receiver := newMetricsReceiver(t)
	provider := receiver.provider(t)
	var calls atomic.Int64
	_, err := provider.Meter("shutdown").Int64ObservableCounter("jobs_total", metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
		o.Observe(calls.Add(1)*3, primary)
		return nil
	}))
	require.NoError(t, err)

	// When
	require.NoError(t, provider.Shutdown(context.Background()))

	// Then
	require.Equal(t, float64(3), receiver.await(t, "jobs_total{pool=primary}").value)
	require.Error(t, provider.ForceFlush(context.Background()))
	require.Equal(t, int64(1), calls.Load())
}

func TestObservableInstruments_ignore_the_span_of_the_collecting_context(t *testing.T) {
	// Given: ForceFlush is called under a sampled span
	receiver := newMetricsReceiver(t)
	provider := receiver.provider(t)
	_, err := provider.Meter("span").Int64ObservableCounter("ticks_total", metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
		o.Observe(5, primary)
		return nil
	}))
	require.NoError(t, err)
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}, TraceFlags: trace.FlagsSampled,
	}))

	// When
	require.NoError(t, provider.ForceFlush(ctx))

	// Then
	point := receiver.await(t, "ticks_total{pool=primary}")
	require.Equal(t, float64(5), point.value)
	require.Empty(t, point.exemplars)
}

func TestMeter_sync_counter_and_histogram_export_exemplar_of_sampled_span(t *testing.T) {
	// Given
	receiver := newMetricsReceiver(t)
	provider := receiver.provider(t)
	meter := provider.Meter("sync")
	counter, err := meter.Int64Counter("orders_total")
	require.NoError(t, err)
	histogram, err := meter.Float64Histogram("order_seconds")
	require.NoError(t, err)
	traceID := trace.TraceID{0xde, 0xad, 0xbe, 0xef, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	spanID := trace.SpanID{0xca, 0xfe, 1, 2, 3, 4, 5, 6}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	}))

	// When
	counter.Add(ctx, 2, primary)
	histogram.Record(ctx, 0.4, primary)
	require.NoError(t, provider.ForceFlush(context.Background()))

	// Then
	for _, series := range []string{"orders_total{pool=primary}", "order_seconds{pool=primary}"} {
		point := receiver.await(t, series)
		require.Len(t, point.exemplars, 1, series)
		require.Equal(t, traceID[:], point.exemplars[0].GetTraceId(), series)
		require.Equal(t, spanID[:], point.exemplars[0].GetSpanId(), series)
	}
}

func TestMeter_observable_registration_is_safe_during_concurrent_collection(t *testing.T) {
	// Given
	receiver := newMetricsReceiver(t)
	provider := receiver.provider(t)
	meter := provider.Meter("concurrent")
	gauge, err := meter.Int64ObservableGauge("workers")
	require.NoError(t, err)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				_ = provider.ForceFlush(context.Background())
			}
		}
	})

	// When
	for range 50 {
		registration, err := meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
			o.ObserveInt64(gauge, 1, primary)
			return nil
		}, gauge)
		require.NoError(t, err)
		require.NoError(t, registration.Unregister())
	}
	close(stop)
	wg.Wait()

	// Then: a callback registered after the churn is collected
	_, err = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		o.ObserveInt64(gauge, 3, primary)
		return nil
	}, gauge)
	require.NoError(t, err)
	require.NoError(t, provider.ForceFlush(context.Background()))
	require.Equal(t, float64(3), receiver.await(t, "workers{pool=primary}").value)
}

func TestWithCollectionInterval_rejects_non_positive_interval(t *testing.T) {
	_, err := NewMeterProvider(WithCollectionInterval(0))
	require.ErrorIs(t, err, ErrCollectionInterval)
}
