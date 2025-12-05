package stats

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/convoy-road-trips-app/stats/transport"
)

func TestPipeline_DropStrategies(t *testing.T) {
	// Setup pipeline with small buffer
	cfg := DefaultConfig()
	cfg.BufferSize = 4 // Small buffer
	cfg.Workers = 0    // No workers to drain buffer

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Helper to create pipeline
	createPipeline := func(strategy DropStrategy) *Pipeline {
		cfg.DropStrategy = strategy
		return &Pipeline{
			cfg:            cfg,
			buffer:         transport.NewRingBuffer(cfg.BufferSize),
			workers:        0,
			exporters:      []Exporter{},
			exporterErrors: make([]atomic.Uint64, 0),
			ctx:            ctx,
			cancel:         cancel,
			shutdownCh:     make(chan struct{}),
		}
	}

	t.Run("DropNewest", func(t *testing.T) {
		p := createPipeline(DropNewest)

		// Fill buffer
		for i := 0; i < 4; i++ {
			if err := p.Record(ctx, &Metric{Name: "fill", Value: float64(i)}); err != nil {
				t.Fatalf("Failed to fill buffer at %d: %v", i, err)
			}
		}

		// Try to record one more
		err := p.Record(ctx, &Metric{Name: "new", Value: 100})
		if err != ErrBufferFull {
			t.Errorf("Expected ErrBufferFull, got %v", err)
		}

		// Verify dropped count
		if p.dropped.Load() != 1 {
			t.Errorf("Expected 1 dropped metric, got %d", p.dropped.Load())
		}
	})

	t.Run("DropOldest", func(t *testing.T) {
		p := createPipeline(DropOldest)

		// Fill buffer
		for i := 0; i < 4; i++ {
			if err := p.Record(ctx, &Metric{Name: "fill", Value: float64(i)}); err != nil {
				t.Fatalf("Failed to fill buffer at %d: %v", i, err)
			}
		}

		// Try to record one more
		// Should succeed by dropping the oldest
		err := p.Record(ctx, &Metric{Name: "new", Value: 100})
		if err != nil {
			t.Errorf("Expected success, got %v", err)
		}

		// Verify buffer content (should contain 1, 2, 3, new)
		// 0 should be gone
		// Since we can't easily peek, we pop all
		items := p.buffer.PopBatch(10)
		if len(items) != 4 {
			t.Fatalf("Expected 4 items, got %d", len(items))
		}

		// First item should be 1 (since 0 was dropped)
		first := items[0].(*Metric)
		if first.Value != 1.0 {
			t.Errorf("Expected first item value 1.0, got %f", first.Value)
		}

		// Last item should be new
		last := items[3].(*Metric)
		if last.Name != "new" {
			t.Errorf("Expected last item name 'new', got %s", last.Name)
		}
	})
}

func TestPipeline_AdaptiveBatching(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BufferSize = 1024
	cfg.AdaptiveBatching = true
	cfg.FlushInterval = 100 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Mock buffer to control Len()
	// Since we can't easily mock the struct method in Go without interface,
	// we'll rely on filling the real buffer.

	p := &Pipeline{
		cfg:            cfg,
		buffer:         transport.NewRingBuffer(cfg.BufferSize),
		workers:        1,
		exporters:      []Exporter{},
		exporterErrors: make([]atomic.Uint64, 0),
		ctx:            ctx,
		cancel:         cancel,
		shutdownCh:     make(chan struct{}),
	}

	// Fill buffer > 50% (512 items)
	// We need to prevent worker from draining it immediately
	// But worker is started in Start(), which we haven't called yet.
	// We will call worker() manually.

	for i := 0; i < 600; i++ {
		p.buffer.Push(&Metric{Name: "test", Value: 1.0})
	}

	// We need to verify that worker picks up > 100 items.
	// We can't easily spy on local variable `batchSize` in worker.
	// But we can verify `PopBatch` behavior if we could spy on it.

	// Alternative: Check if `processBatch` receives a batch > 100.
	// We can mock `processBatch`? No, it's a method.
	// We can mock Exporter and count batch size!

	var maxBatchSize int
	var batchCount int

	mockExporter := &MockExporter{
		name: "mock",
		exportFunc: func(ctx context.Context, metrics []*Metric) error {
			if len(metrics) > maxBatchSize {
				maxBatchSize = len(metrics)
			}
			batchCount++
			return nil
		},
	}
	p.exporters = []Exporter{mockExporter}
	p.exporterErrors = make([]atomic.Uint64, 1)

	// Run worker for a short time
	p.wg.Add(1)
	go p.worker(0)

	// Wait for worker to process
	time.Sleep(200 * time.Millisecond)
	p.cancel()
	p.wg.Wait()

	// Verify
	if maxBatchSize <= 100 {
		t.Errorf("Expected max batch size > 100 (adaptive), got %d", maxBatchSize)
	}
}
