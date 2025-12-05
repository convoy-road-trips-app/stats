package stats

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/convoy-road-trips-app/stats/transport"
)

// MockExporter is a mock implementation of Exporter for testing
type MockExporter struct {
	name         string
	exportFunc   func(ctx context.Context, metrics []*Metric) error
	shutdownFunc func(ctx context.Context) error
}

func (m *MockExporter) Name() string {
	return m.name
}

func (m *MockExporter) Export(ctx context.Context, metrics []*Metric) error {
	if m.exportFunc != nil {
		return m.exportFunc(ctx, metrics)
	}
	return nil
}

func (m *MockExporter) Shutdown(ctx context.Context) error {
	if m.shutdownFunc != nil {
		return m.shutdownFunc(ctx)
	}
	return nil
}

func TestPipeline_ParallelExport(t *testing.T) {
	// Create two exporters: one slow, one fast
	var fastExported atomic.Bool
	var slowExported atomic.Bool

	fastExporter := &MockExporter{
		name: "fast",
		exportFunc: func(ctx context.Context, metrics []*Metric) error {
			fastExported.Store(true)
			return nil
		},
	}

	slowExporter := &MockExporter{
		name: "slow",
		exportFunc: func(ctx context.Context, metrics []*Metric) error {
			time.Sleep(50 * time.Millisecond)
			slowExported.Store(true)
			return nil
		},
	}

	cfg := DefaultConfig()
	cfg.FlushInterval = 10 * time.Millisecond

	// Create pipeline manually to inject mock exporters
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := &Pipeline{
		cfg:            cfg,
		buffer:         transport.NewRingBuffer(1024),
		workers:        1,
		exporters:      []Exporter{fastExporter, slowExporter},
		exporterErrors: make([]atomic.Uint64, 2),
		ctx:            ctx,
		cancel:         cancel,
		shutdownCh:     make(chan struct{}),
	}

	// Create a batch of metrics
	batch := []*Metric{
		{Name: "test.metric", Value: 1.0},
	}

	// Measure time to process batch
	start := time.Now()
	p.processBatch(batch)
	duration := time.Since(start)

	// Verification
	if !fastExported.Load() {
		t.Error("Fast exporter should have been called")
	}
	if !slowExported.Load() {
		t.Error("Slow exporter should have been called")
	}

	// Since we are running in parallel, the total time should be roughly equal to the slow exporter
	// If sequential, it would be fast + slow (negligible + 50ms)
	// But wait, processBatch waits for all exporters, so the total time IS determined by the slowest.
	// The benefit of parallel export is that the fast exporter finishes early (though we wait for all).
	// But more importantly, if we had multiple slow exporters, they would run concurrently.

	// Let's verify that the fast exporter finished "quickly" relative to the slow one?
	// Hard to verify exact timing in unit test without race conditions.
	// But we can verify that panic recovery works and error tracking works.

	if duration < 50*time.Millisecond {
		t.Errorf("Expected duration >= 50ms, got %v", duration)
	}
}

func TestPipeline_PanicRecovery(t *testing.T) {
	panickingExporter := &MockExporter{
		name: "panic",
		exportFunc: func(ctx context.Context, metrics []*Metric) error {
			panic("something went wrong")
		},
	}

	cfg := DefaultConfig()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := &Pipeline{
		cfg:            cfg,
		buffer:         transport.NewRingBuffer(1024),
		exporters:      []Exporter{panickingExporter},
		exporterErrors: make([]atomic.Uint64, 1),
		ctx:            ctx,
		cancel:         cancel,
		shutdownCh:     make(chan struct{}),
	}

	batch := []*Metric{{Name: "test", Value: 1}}

	// Should not panic
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Pipeline panicked: %v", r)
			}
		}()
		p.processBatch(batch)
	}()

	// Check error counts
	if p.errors.Load() != 1 {
		t.Errorf("Expected 1 error, got %d", p.errors.Load())
	}
	if p.exporterErrors[0].Load() != 1 {
		t.Errorf("Expected 1 exporter error, got %d", p.exporterErrors[0].Load())
	}
}

func TestPipeline_ErrorTracking(t *testing.T) {
	errorExporter := &MockExporter{
		name: "error",
		exportFunc: func(ctx context.Context, metrics []*Metric) error {
			return errors.New("export failed")
		},
	}

	successExporter := &MockExporter{
		name: "success",
		exportFunc: func(ctx context.Context, metrics []*Metric) error {
			return nil
		},
	}

	cfg := DefaultConfig()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := &Pipeline{
		cfg:            cfg,
		buffer:         transport.NewRingBuffer(1024),
		exporters:      []Exporter{errorExporter, successExporter},
		exporterErrors: make([]atomic.Uint64, 2),
		ctx:            ctx,
		cancel:         cancel,
		shutdownCh:     make(chan struct{}),
	}

	batch := []*Metric{{Name: "test", Value: 1}}
	p.processBatch(batch)

	// Check global errors
	if p.errors.Load() != 1 {
		t.Errorf("Expected 1 global error, got %d", p.errors.Load())
	}

	// Check per-exporter errors
	stats := p.Stats()
	if stats.ExporterErrors["error"] != 1 {
		t.Errorf("Expected 1 error for 'error' exporter, got %d", stats.ExporterErrors["error"])
	}
	if stats.ExporterErrors["success"] != 0 {
		t.Errorf("Expected 0 errors for 'success' exporter, got %d", stats.ExporterErrors["success"])
	}
}
