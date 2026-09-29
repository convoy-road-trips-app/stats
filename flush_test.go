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
)

// flushGuard bounds how long a test waits for a call that must return on its own.
const flushGuard = 5 * time.Second

type ctxKey struct{}

// exportCounter counts exported observations by metric name.
type exportCounter struct {
	mu     sync.Mutex
	counts map[string]float64
}

func (c *exportCounter) export(_ context.Context, ms []*Metric) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.counts == nil {
		c.counts = make(map[string]float64)
	}
	for _, m := range ms {
		if m.Name == droppedLabelsMetric {
			c.counts[m.Name] += m.Value
			continue
		}
		c.counts[m.Name]++
	}
	return nil
}

func (c *exportCounter) count(name string) float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[name]
}

// startedPipeline starts a pipeline with the given workers and export function.
// The flush ticker is effectively disabled so only full batches, Flush and
// Shutdown export.
func startedPipeline(t *testing.T, workers int, export func(context.Context, []*Metric) error) *Pipeline {
	t.Helper()
	cfg := DefaultConfig()
	cfg.FlushInterval = time.Hour
	p := newUnstartedPipeline(t, cfg)
	p.workers = workers
	p.exporters = []Exporter{&MockExporter{name: "capture", exportFunc: export}}
	p.exporterErrors = make([]atomic.Uint64, 1)
	require.NoError(t, p.Start())
	return p
}

// within runs call and fails the test if it does not return within flushGuard.
func within(t *testing.T, call func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- call() }()
	select {
	case err := <-done:
		return err
	case <-time.After(flushGuard):
		t.Fatalf("call did not return within %v", flushGuard)
		return nil
	}
}

func TestPipeline_Flush_exports_buffered_and_worker_batch_observations_before_returning(t *testing.T) {
	// Given: 250 accepted observations, some still in the ring and some in partial worker batches
	counter := &exportCounter{}
	p := startedPipeline(t, 2, counter.export)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	for range 250 {
		require.NoError(t, p.Record(context.Background(), observation("jobs_total")))
	}

	// When
	err := within(t, func() error { return p.Flush(context.Background()) })

	// Then
	require.NoError(t, err)
	require.Equal(t, float64(250), counter.count("jobs_total"))
}

func TestPipeline_Flush_exports_pending_drop_counters(t *testing.T) {
	// Given: one series overflow pending in the drop counter
	counter := &exportCounter{}
	p := startedPipeline(t, 1, counter.export)
	p.cfg.MaxCardinality = 1
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	require.NoError(t, p.Record(context.Background(), observation("jobs_total", attribute.Int("id", 0))))
	require.ErrorIs(t, p.Record(context.Background(), observation("jobs_total", attribute.Int("id", 1))), ErrCardinalityLimit)

	// When
	err := within(t, func() error { return p.Flush(context.Background()) })

	// Then
	require.NoError(t, err)
	require.Equal(t, float64(1), counter.count(droppedLabelsMetric))
}

func TestPipeline_Flush_passes_caller_context_to_exporters(t *testing.T) {
	// Given
	var seen atomic.Value
	p := startedPipeline(t, 1, func(ctx context.Context, _ []*Metric) error {
		seen.Store(ctx.Value(ctxKey{}))
		return nil
	})
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	require.NoError(t, p.Record(context.Background(), observation("jobs_total")))

	// When
	err := within(t, func() error { return p.Flush(context.WithValue(context.Background(), ctxKey{}, "invocation-1")) })

	// Then
	require.NoError(t, err)
	require.Equal(t, "invocation-1", seen.Load())
}

func TestPipeline_Flush_returns_deadline_exceeded_when_exporter_blocks(t *testing.T) {
	// Given: an exporter that ignores its context and blocks until released
	release := make(chan struct{})
	p := startedPipeline(t, 1, func(context.Context, []*Metric) error {
		<-release
		return nil
	})
	t.Cleanup(func() { close(release); _ = p.Shutdown(context.Background()) })
	require.NoError(t, p.Record(context.Background(), observation("jobs_total")))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// When
	err := within(t, func() error { return p.Flush(ctx) })

	// Then
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestPipeline_Flush_returns_exporter_error(t *testing.T) {
	// Given
	exportErr := errors.New("collector unavailable")
	p := startedPipeline(t, 1, func(context.Context, []*Metric) error { return exportErr })
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	require.NoError(t, p.Record(context.Background(), observation("jobs_total")))

	// When
	err := within(t, func() error { return p.Flush(context.Background()) })

	// Then
	require.ErrorIs(t, err, exportErr)
}

func TestPipeline_Flush_after_shutdown_returns_client_closed(t *testing.T) {
	// Given
	p := startedPipeline(t, 1, func(context.Context, []*Metric) error { return nil })
	require.NoError(t, p.Shutdown(context.Background()))

	// When
	err := within(t, func() error { return p.Flush(context.Background()) })

	// Then
	require.ErrorIs(t, err, ErrClientClosed)
}

func TestPipeline_concurrent_Flush_and_Record_export_every_accepted_observation(t *testing.T) {
	// Given
	counter := &exportCounter{}
	p := startedPipeline(t, 4, counter.export)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	var accepted atomic.Int64
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 500 {
				if p.Record(context.Background(), observation("jobs_total")) == nil {
					accepted.Add(1)
				}
			}
			_ = p.Flush(context.Background())
		})
	}
	wg.Wait()

	// When
	err := within(t, func() error { return p.Flush(context.Background()) })

	// Then
	require.NoError(t, err)
	require.Equal(t, float64(accepted.Load()), counter.count("jobs_total"))
}

func TestPipeline_Shutdown_exports_all_10k_accepted_observations(t *testing.T) {
	// Given: 10k observations accepted from concurrent callers
	counter := &exportCounter{}
	p := startedPipeline(t, 4, counter.export)
	var accepted atomic.Int64
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 1250 {
				if p.Record(context.Background(), observation("jobs_total")) == nil {
					accepted.Add(1)
				}
			}
		})
	}
	wg.Wait()
	require.Equal(t, int64(10_000), accepted.Load())

	// When
	err := within(t, func() error { return p.Shutdown(context.Background()) })

	// Then
	require.NoError(t, err)
	require.Equal(t, float64(10_000), counter.count("jobs_total"))
}

func TestPipeline_Shutdown_exports_ring_entries_workers_have_not_popped(t *testing.T) {
	// Given: observations accepted before any worker runs
	counter := &exportCounter{}
	cfg := DefaultConfig()
	cfg.FlushInterval = time.Hour
	p := newUnstartedPipeline(t, cfg)
	p.workers = 4
	p.exporters = []Exporter{&MockExporter{name: "capture", exportFunc: counter.export}}
	p.exporterErrors = make([]atomic.Uint64, 1)
	for range 10_000 {
		require.NoError(t, p.Record(context.Background(), observation("jobs_total")))
	}
	require.NoError(t, p.Start())

	// When
	err := within(t, func() error { return p.Shutdown(context.Background()) })

	// Then
	require.NoError(t, err)
	require.Equal(t, float64(10_000), counter.count("jobs_total"))
}

func TestPipeline_Shutdown_passes_caller_context_to_exporters(t *testing.T) {
	// Given
	var seen atomic.Value
	p := startedPipeline(t, 1, func(ctx context.Context, _ []*Metric) error {
		seen.Store(ctx.Value(ctxKey{}))
		return nil
	})
	require.NoError(t, p.Record(context.Background(), observation("jobs_total")))

	// When
	err := within(t, func() error { return p.Shutdown(context.WithValue(context.Background(), ctxKey{}, "shutdown")) })

	// Then
	require.NoError(t, err)
	require.Equal(t, "shutdown", seen.Load())
}

func TestPipeline_Shutdown_returns_deadline_exceeded_when_drain_blocks(t *testing.T) {
	// Given: an exporter that ignores its context and blocks until released
	release := make(chan struct{})
	p := startedPipeline(t, 1, func(context.Context, []*Metric) error {
		<-release
		return nil
	})
	t.Cleanup(func() { close(release) })
	require.NoError(t, p.Record(context.Background(), observation("jobs_total")))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// When
	err := within(t, func() error { return p.Shutdown(ctx) })

	// Then
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
