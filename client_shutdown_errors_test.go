package stats

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats/runtimemetrics"
)

var errExporterShutdown = errors.New("exporter shutdown failed")

// shutdownExporter counts Shutdown calls and returns err from each.
type shutdownExporter struct {
	calls atomic.Int32
	err   error
}

func (e *shutdownExporter) exporter() *MockExporter {
	return &MockExporter{name: "lifecycle", shutdownFunc: func(context.Context) error {
		e.calls.Add(1)
		return e.err
	}}
}

// lifecycleClient builds a client over a started pipeline with the given
// workers and exporter, plus collector (nil for none).
func lifecycleClient(t *testing.T, workers int, exporter *MockExporter, collector *runtimemetrics.Collector) *Client {
	t.Helper()
	cfg := DefaultConfig()
	cfg.FlushInterval = time.Hour
	p := newUnstartedPipeline(t, cfg)
	p.workers = workers
	p.exporters = []Exporter{exporter}
	p.exporterErrors = make([]atomic.Uint64, 1)
	require.NoError(t, p.Start())
	return &Client{core: &clientCore{cfg: cfg, pipeline: p, collector: collector}, root: true}
}

// stuckCollector starts a collector whose background goroutine blocks inside
// its record callback, so Stop can only return ctx's error. The goroutine is
// released when the test ends.
func stuckCollector(t *testing.T) *runtimemetrics.Collector {
	t.Helper()
	var armed atomic.Bool
	var enteredOnce sync.Once
	entered := make(chan struct{})
	release := make(chan struct{})
	collector := runtimemetrics.New(runtimemetrics.Config{CollectInterval: time.Millisecond},
		func(string, MetricType, float64, ...attribute.KeyValue) {
			if !armed.Load() {
				return // the synchronous sample taken by Start
			}
			enteredOnce.Do(func() { close(entered) })
			<-release
		})
	collector.Start()
	armed.Store(true)
	t.Cleanup(func() {
		close(release)
		require.NoError(t, collector.Stop(context.Background()))
	})
	select {
	case <-entered:
	case <-time.After(flushGuard):
		t.Fatalf("collector goroutine did not sample within %v", flushGuard)
	}
	return collector
}

func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestClientShutdown_returns_nil_when_collector_and_pipeline_stop_cleanly(t *testing.T) {
	// Given: a running collector and an exporter that shuts down cleanly
	collector := runtimemetrics.New(runtimemetrics.Config{CollectInterval: time.Millisecond}, nil)
	collector.Start()
	exporter := &shutdownExporter{}
	client := lifecycleClient(t, 2, exporter.exporter(), collector)

	// When: the client shuts down
	err := within(t, func() error { return client.Shutdown(context.Background()) })

	// Then: no error is reported
	require.NoError(t, err)
}

func TestClientShutdown_returns_only_the_pipeline_error_when_only_the_pipeline_fails(t *testing.T) {
	// Given: no collector and an exporter whose shutdown fails
	exporter := &shutdownExporter{err: errExporterShutdown}
	client := lifecycleClient(t, 2, exporter.exporter(), nil)

	// When: the client shuts down
	err := within(t, func() error { return client.Shutdown(context.Background()) })

	// Then: the pipeline error is returned with its own wrapping and nothing else
	require.ErrorIs(t, err, errExporterShutdown)
	require.EqualError(t, err, "pipeline shutdown: exporter lifecycle shutdown: exporter shutdown failed")
}

func TestClientShutdown_returns_only_the_collector_error_when_only_the_collector_fails(t *testing.T) {
	// Given: a collector that cannot stop before ctx is done and a pipeline
	// without workers or failing exporters, so it cannot fail on ctx
	exporter := &shutdownExporter{}
	client := lifecycleClient(t, 0, exporter.exporter(), stuckCollector(t))

	// When: the client shuts down with a canceled context
	err := within(t, func() error { return client.Shutdown(canceledContext()) })

	// Then: the collector error is returned as the single wrapped error
	require.ErrorIs(t, err, context.Canceled)
	require.EqualError(t, err, "stop runtime collector: context canceled")
	require.Equal(t, context.Canceled, errors.Unwrap(err))
	require.EqualValues(t, 1, exporter.calls.Load(), "pipeline shutdown must still run")
}

func TestClientShutdown_reports_both_errors_when_collector_and_pipeline_fail(t *testing.T) {
	// Given: a collector that cannot stop before ctx is done and a pipeline
	// whose exporter shutdown fails; without workers the pipeline's only
	// error is the exporter's, so each sentinel has exactly one source
	exporter := &shutdownExporter{err: errExporterShutdown}
	client := lifecycleClient(t, 0, exporter.exporter(), stuckCollector(t))

	// When: the client shuts down with a canceled context
	err := within(t, func() error { return client.Shutdown(canceledContext()) })

	// Then: both the collector error and the pipeline error are detectable
	require.ErrorIs(t, err, context.Canceled, "collector error lost")
	require.ErrorIs(t, err, errExporterShutdown, "pipeline error lost")
}

func TestClientShutdown_still_cleans_up_when_collector_and_pipeline_fail(t *testing.T) {
	// Given: a failing collector stop and a failing exporter shutdown
	exporter := &shutdownExporter{err: errExporterShutdown}
	client := lifecycleClient(t, 0, exporter.exporter(), stuckCollector(t))

	// When: the client shuts down with a canceled context
	_ = within(t, func() error { return client.Shutdown(canceledContext()) })

	// Then: the client is closed, the pipeline is stopped and the exporter shut down once
	require.ErrorIs(t, client.Counter(context.Background(), "after.shutdown", 1), ErrClientClosed)
	require.ErrorIs(t, client.core.pipeline.ctx.Err(), context.Canceled)
	require.EqualValues(t, 1, exporter.calls.Load())
}

func TestClientShutdown_returns_nil_on_repeat_call_after_failure(t *testing.T) {
	// Given: a client whose first shutdown failed
	exporter := &shutdownExporter{err: errExporterShutdown}
	client := lifecycleClient(t, 0, exporter.exporter(), stuckCollector(t))
	require.Error(t, within(t, func() error { return client.Shutdown(canceledContext()) }))

	// When: shutdown is called again
	err := client.Shutdown(context.Background())

	// Then: it is a no-op that reports nothing and does not shut exporters down again
	require.NoError(t, err)
	require.EqualValues(t, 1, exporter.calls.Load())
}
