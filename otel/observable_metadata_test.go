package otel

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

func TestObservableInstruments_export_description_and_unit_on_every_export(t *testing.T) {
	// Given: every observable kind created with a description and a unit
	receiver := newMetricsReceiver(t)
	provider := receiver.provider(t)
	meter := provider.Meter("observable")
	var round atomic.Int64
	observed := func() int64 { return 10 + 15*round.Load() }
	int64Callback := metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
		o.Observe(observed(), primary)
		return nil
	})
	float64Callback := metric.WithFloat64Callback(func(_ context.Context, o metric.Float64Observer) error {
		o.Observe(float64(observed()), primary)
		return nil
	})
	create := map[string]func(description, unit string) error{
		"conns_opened_total": func(d, u string) error {
			_, err := meter.Int64ObservableCounter("conns_opened_total", metric.WithDescription(d), metric.WithUnit(u), int64Callback)
			return err
		},
		"cpu_seconds_total": func(d, u string) error {
			_, err := meter.Float64ObservableCounter("cpu_seconds_total", metric.WithDescription(d), metric.WithUnit(u), float64Callback)
			return err
		},
		"queue_depth": func(d, u string) error {
			_, err := meter.Int64ObservableUpDownCounter("queue_depth", metric.WithDescription(d), metric.WithUnit(u), int64Callback)
			return err
		},
		"balance": func(d, u string) error {
			_, err := meter.Float64ObservableUpDownCounter("balance", metric.WithDescription(d), metric.WithUnit(u), float64Callback)
			return err
		},
		"conns_open": func(d, u string) error {
			_, err := meter.Int64ObservableGauge("conns_open", metric.WithDescription(d), metric.WithUnit(u), int64Callback)
			return err
		},
		"pool_utilization": func(d, u string) error {
			_, err := meter.Float64ObservableGauge("pool_utilization", metric.WithDescription(d), metric.WithUnit(u), float64Callback)
			return err
		},
	}
	for name, newInstrument := range create {
		require.NoError(t, newInstrument(name+" description", name+"_unit"))
	}

	for r := range int64(2) {
		round.Store(r)

		// When
		require.NoError(t, provider.ForceFlush(context.Background()))

		// Then: metadata is on the wire and the callback values keep their task-7 semantics
		exported := latestWireMetrics(receiver)
		for name := range create {
			m, ok := exported[name]
			require.True(t, ok, "round %d: %s missing", r, name)
			require.Equal(t, name+" description", m.GetDescription(), name)
			require.Equal(t, name+"_unit", m.GetUnit(), name)
			require.InDelta(t, float64(observed()), receiver.await(t, name+"{pool=primary}").value, 1e-9, name)
		}
	}
}

// latestWireMetrics returns the most recently received metric of each name.
func latestWireMetrics(r *metricsReceiver) map[string]*metricspb.Metric {
	r.mu.Lock()
	defer r.mu.Unlock()
	latest := map[string]*metricspb.Metric{}
	for _, request := range r.log {
		for _, resource := range request.GetResourceMetrics() {
			for _, scope := range resource.GetScopeMetrics() {
				for _, m := range scope.GetMetrics() {
					latest[m.GetName()] = m
				}
			}
		}
	}
	return latest
}
