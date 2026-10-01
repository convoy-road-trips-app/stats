package stats

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// barrierExporter blocks the first background export (one that does not carry
// ctxKey) until released and then fails it. Exports that carry ctxKey come
// from Flush or Shutdown; they are counted by name and announced on flushed.
type barrierExporter struct {
	inFlightErr error
	entered     chan struct{} // closed when the blocked background export starts
	release     chan struct{} // closed by the test to let it fail
	flushed     chan struct{} // receives once per Flush/Shutdown export
	blocked     atomic.Bool
	counter     exportCounter
}

func newBarrierExporter() *barrierExporter {
	return &barrierExporter{
		inFlightErr: errors.New("background export timed out"),
		entered:     make(chan struct{}),
		release:     make(chan struct{}),
		flushed:     make(chan struct{}, 16),
	}
}

func (b *barrierExporter) export(ctx context.Context, ms []*Metric) error {
	if ctx.Value(ctxKey{}) != nil {
		_ = b.counter.export(ctx, ms)
		b.flushed <- struct{}{}
		return nil
	}
	if b.blocked.CompareAndSwap(false, true) {
		close(b.entered)
		<-b.release
		return b.inFlightErr
	}
	return nil
}

// pipelineWithBlockedWorker1 returns a pipeline whose worker 1 is blocked
// inside a background export of 100 observations accepted before the test's
// Flush/Shutdown call, and whose worker 0 is running normally.
func pipelineWithBlockedWorker1(t *testing.T, exporter *barrierExporter) *Pipeline {
	t.Helper()
	cfg := DefaultConfig()
	cfg.FlushInterval = time.Hour
	p := newUnstartedPipeline(t, cfg)
	p.workers = 2
	p.exporters = []Exporter{&MockExporter{name: "barrier", exportFunc: exporter.export}}
	p.exporterErrors = make([]atomic.Uint64, 1)
	p.flushes = []chan flushRequest{make(chan flushRequest), make(chan flushRequest)}
	p.wg.Add(1)
	go p.worker(1)
	for range 100 {
		require.NoError(t, p.Record(context.Background(), observation("in_flight_total")))
	}
	select {
	case <-exporter.entered:
	case <-time.After(flushGuard):
		t.Fatal("worker 1 never started its background export")
	}
	p.wg.Add(1)
	go p.worker(0)
	for range 3 {
		require.NoError(t, p.Record(context.Background(), observation("pre_call_total")))
	}
	return p
}

func TestPipeline_Flush_reports_failed_in_flight_export_of_pre_call_observations_while_worker_0_resumes(t *testing.T) {
	// Given: worker 1 holds 100 pre-call observations in a blocked background export
	exporter := newBarrierExporter()
	p := pipelineWithBlockedWorker1(t, exporter)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	ctx := context.WithValue(context.Background(), ctxKey{}, "invocation")
	result := make(chan error, 1)

	// When: Flush reaches worker 0, which exports and resumes its loop while
	// worker 1 is still blocked; then worker 1's in-flight export fails
	go func() { result <- p.Flush(ctx) }()
	<-exporter.flushed
	require.NoError(t, p.Record(context.Background(), observation("post_call_total")))
	close(exporter.release)
	err := within(t, func() error { return <-result })

	// Then: the pre-call observations that were buffered went out with the caller
	// ctx, and the lost in-flight ones make Flush fail instead of reporting success
	require.InDelta(t, float64(3), exporter.counter.count("pre_call_total"), 0.001)
	require.ErrorIs(t, err, exporter.inFlightErr)
}

func TestPipeline_Shutdown_reports_failed_in_flight_export_of_pre_call_observations(t *testing.T) {
	// Given
	exporter := newBarrierExporter()
	p := pipelineWithBlockedWorker1(t, exporter)
	ctx := context.WithValue(context.Background(), ctxKey{}, "shutdown")
	result := make(chan error, 1)

	// When
	go func() { result <- p.Shutdown(ctx) }()
	<-exporter.flushed
	close(exporter.release)
	err := within(t, func() error { return <-result })

	// Then
	require.InDelta(t, float64(3), exporter.counter.count("pre_call_total"), 0.001)
	require.ErrorIs(t, err, exporter.inFlightErr)
}

func TestPipeline_Flush_does_not_report_background_failures_that_ended_before_the_call(t *testing.T) {
	// Given: a background export failed and finished before Flush was called
	exporter := newBarrierExporter()
	p := pipelineWithBlockedWorker1(t, exporter)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	close(exporter.release)
	// This Flush may or may not overlap the failing export; once it returns,
	// worker 1 has handled its request, so the failure is over.
	_ = within(t, func() error { return p.Flush(context.Background()) })

	// When
	err := within(t, func() error { return p.Flush(context.WithValue(context.Background(), ctxKey{}, "next")) })

	// Then
	require.NoError(t, err)
}

func TestPipeline_Shutdown_exports_observations_whose_Record_raced_with_shutdown(t *testing.T) {
	for range 200 {
		// Given: producers recording until the pipeline reports it is closed
		counter := &exportCounter{}
		p := startedPipeline(t, 2, counter.export)
		var accepted atomic.Int64
		started := make(chan struct{})
		var once sync.Once
		var producers sync.WaitGroup
		for range 4 {
			producers.Go(func() {
				for {
					if err := p.Record(context.Background(), observation("jobs_total")); err != nil {
						if errors.Is(err, ErrClientClosed) {
							return
						}
						continue
					}
					if accepted.Add(1) == 64 {
						once.Do(func() { close(started) })
					}
				}
			})
		}
		<-started

		// When
		err := within(t, func() error { return p.Shutdown(context.Background()) })
		producers.Wait()

		// Then: every Record that returned nil was exported
		require.NoError(t, err)
		require.InDelta(t, float64(accepted.Load()), counter.count("jobs_total"), 0.001)
	}
}

func TestPipeline_Shutdown_exports_a_completely_full_ring(t *testing.T) {
	// Given: a ring filled to capacity before any worker runs
	counter := &exportCounter{}
	cfg := DefaultConfig()
	cfg.FlushInterval = time.Hour
	cfg.BufferSize = 8
	p := newUnstartedPipeline(t, cfg)
	p.workers = 2
	p.exporters = []Exporter{&MockExporter{name: "capture", exportFunc: counter.export}}
	p.exporterErrors = make([]atomic.Uint64, 1)
	for range 8 {
		require.NoError(t, p.Record(context.Background(), observation("jobs_total")))
	}
	require.ErrorIs(t, p.Record(context.Background(), observation("jobs_total")), ErrBufferFull)
	require.NoError(t, p.Start())

	// When
	err := within(t, func() error { return p.Shutdown(context.Background()) })

	// Then
	require.NoError(t, err)
	require.InDelta(t, float64(8), counter.count("jobs_total"), 0.001)
}

func TestPipeline_Flush_returns_while_producers_keep_filling_the_ring(t *testing.T) {
	// Given: pre-call observations, then a producer that never stops recording
	counter := &exportCounter{}
	cfg := DefaultConfig()
	cfg.FlushInterval = time.Hour
	cfg.BufferSize = 64
	p := newUnstartedPipeline(t, cfg)
	p.workers = 2
	p.exporters = []Exporter{&MockExporter{name: "capture", exportFunc: counter.export}}
	p.exporterErrors = make([]atomic.Uint64, 1)
	for range 64 {
		require.NoError(t, p.Record(context.Background(), observation("pre_call_total")))
	}
	require.NoError(t, p.Start())
	stop := make(chan struct{})
	var producer sync.WaitGroup
	producer.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				_ = p.Record(context.Background(), observation("live_total"))
			}
		}
	})
	t.Cleanup(func() { close(stop); producer.Wait(); _ = p.Shutdown(context.Background()) })

	// When
	err := within(t, func() error { return p.Flush(context.Background()) })

	// Then
	require.NoError(t, err)
	require.InDelta(t, float64(64), counter.count("pre_call_total"), 0.001)
}
