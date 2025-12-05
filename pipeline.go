package stats

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/convoy-road-trips-app/stats/exporters/cloudwatch"
	"github.com/convoy-road-trips-app/stats/exporters/datadog"
	"github.com/convoy-road-trips-app/stats/exporters/prometheus"
	"github.com/convoy-road-trips-app/stats/transport"
)

// Pipeline processes metrics asynchronously using a worker pool pattern
type Pipeline struct {
	// Configuration
	cfg *Config

	// Ring buffer for non-blocking metric collection
	buffer *transport.RingBuffer

	// Worker pool
	workers int
	wg      sync.WaitGroup

	// Exporters for different backends
	exporters []Exporter

	// Context for cancellation
	ctx    context.Context
	cancel context.CancelFunc

	// Shutdown coordination
	shutdownOnce sync.Once
	shutdownCh   chan struct{}

	// Metrics
	processed atomic.Uint64
	dropped   atomic.Uint64
	errors    atomic.Uint64
	memUsage  atomic.Int64

	// Per-exporter error counters (index corresponds to exporters slice)
	exporterErrors []atomic.Uint64
}

// Exporter is the interface for backend exporters
type Exporter interface {
	Name() string
	Export(ctx context.Context, metrics []*Metric) error
	Shutdown(ctx context.Context) error
}

// NewPipeline creates a new metric processing pipeline
func NewPipeline(cfg *Config) (*Pipeline, error) {
	if cfg == nil {
		return nil, fmt.Errorf("%w: config is nil", ErrInvalidConfig)
	}

	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}

	// Create exporters based on configuration
	exporters := make([]Exporter, 0, 3)

	// Create Datadog exporter if enabled
	if cfg.Datadog != nil && cfg.Datadog.Enabled {
		ddExporter, err := datadog.NewExporter(cfg.Datadog)
		if err != nil {
			return nil, fmt.Errorf("create datadog exporter: %w", err)
		}
		exporters = append(exporters, ddExporter)
	}

	// Create Prometheus exporter if enabled
	if cfg.Prometheus != nil && cfg.Prometheus.Enabled {
		promExporter, err := prometheus.NewExporter(cfg.Prometheus)
		if err != nil {
			return nil, fmt.Errorf("create prometheus exporter: %w", err)
		}
		exporters = append(exporters, promExporter)
	}

	// Create CloudWatch exporter if enabled
	if cfg.CloudWatch != nil && cfg.CloudWatch.Enabled {
		cwExporter, err := cloudwatch.NewExporter(cfg.CloudWatch)
		if err != nil {
			return nil, fmt.Errorf("create cloudwatch exporter: %w", err)
		}
		exporters = append(exporters, cwExporter)
	}

	ctx, cancel := context.WithCancel(context.Background())

	p := &Pipeline{
		cfg:            cfg,
		buffer:         transport.NewRingBuffer(cfg.BufferSize),
		workers:        cfg.Workers,
		exporters:      exporters,
		exporterErrors: make([]atomic.Uint64, len(exporters)),
		ctx:            ctx,
		cancel:         cancel,
		shutdownCh:     make(chan struct{}),
	}

	return p, nil
}

// Start starts the worker pool
func (p *Pipeline) Start() error {
	// Start workers
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}

	return nil
}

// Record adds a metric to the pipeline (non-blocking)
func (p *Pipeline) Record(ctx context.Context, m *Metric) error {
	// Check if pipeline is shutting down
	select {
	case <-p.shutdownCh:
		return ErrClientClosed
	default:
	}

	// Set timestamp if not set
	if m.Timestamp.IsZero() {
		m.Timestamp = time.Now()
	}

	// Check memory limit
	size := m.EstimateSize()
	current := p.memUsage.Load()
	if current+size > p.cfg.MaxMemoryBytes {
		p.dropped.Add(1)
		return ErrMemoryLimit
	}

	// Try to push to buffer (non-blocking)
	if !p.buffer.Push(m) {
		// Buffer is full, handle based on drop strategy
		if p.cfg.DropStrategy == DropOldest {
			// Remove oldest item to make room
			p.buffer.Pop()
			// Try pushing again
			if !p.buffer.Push(m) {
				// Still failed (race condition?), drop it
				p.dropped.Add(1)
				return ErrBufferFull
			}
			// Successfully added after dropping oldest
			return nil
		}

		// Default: DropNewest
		p.dropped.Add(1)
		return ErrBufferFull
	}

	// Update memory usage
	p.memUsage.Add(size)

	return nil
}

// worker processes metrics from the buffer
func (p *Pipeline) worker(id int) {
	defer p.wg.Done()

	// Batch buffer for efficient processing
	batch := make([]*Metric, 0, 100)
	ticker := time.NewTicker(p.cfg.FlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			// Flush remaining batch before exiting
			if len(batch) > 0 {
				p.processBatch(batch)
			}
			return

		case <-ticker.C:
			// Flush on timer
			if len(batch) > 0 {
				p.processBatch(batch)
				batch = batch[:0] // Reset slice, keep capacity
			}

		default:
			// Determine batch size based on adaptive batching
			batchSize := 100
			if p.cfg.AdaptiveBatching {
				// If buffer is > 50% full, increase batch size
				if p.buffer.Len() > p.buffer.Cap()/2 {
					batchSize = 500
				}
			}

			// Try to pop a batch from buffer
			items := p.buffer.PopBatch(batchSize)
			if len(items) == 0 {
				// Buffer empty, sleep briefly to avoid busy waiting
				time.Sleep(time.Millisecond)
				continue
			}

			// Convert to metrics and add to batch
			for _, item := range items {
				if m, ok := item.(*Metric); ok {
					batch = append(batch, m)
					// Update memory usage
					p.memUsage.Add(-m.EstimateSize())
				}
			}

			// Flush if batch is full
			if len(batch) >= cap(batch) {
				p.processBatch(batch)
				batch = batch[:0]
			}
		}
	}
}

// processBatch sends a batch of metrics to all exporters
func (p *Pipeline) processBatch(batch []*Metric) {
	if len(batch) == 0 {
		return
	}

	// Create a timeout context for exporting
	ctx, cancel := context.WithTimeout(context.Background(), p.cfg.UDPTimeout)
	defer cancel()

	// Send to each exporter in parallel
	var wg sync.WaitGroup

	for i, exporter := range p.exporters {
		wg.Add(1)
		go func(idx int, exp Exporter) {
			defer wg.Done()

			// Panic recovery
			defer func() {
				if r := recover(); r != nil {
					p.errors.Add(1)
					p.exporterErrors[idx].Add(1)
					// In a real app, we might log the panic stack trace here
					fmt.Printf("panic in exporter %s: %v\n", exp.Name(), r)
				}
			}()

			if err := exp.Export(ctx, batch); err != nil {
				p.errors.Add(1)
				p.exporterErrors[idx].Add(1)
				// Log error but continue with other exporters
				// In production, use a proper logger
				_ = err
			}
		}(i, exporter)
	}

	// Wait for all exporters to finish
	wg.Wait()

	// Update processed count
	p.processed.Add(uint64(len(batch)))

	// Return metrics to pool
	for _, m := range batch {
		ReleaseMetric(m)
	}
}

// Shutdown gracefully shuts down the pipeline
func (p *Pipeline) Shutdown(ctx context.Context) error {
	var shutdownErr error

	p.shutdownOnce.Do(func() {
		// Signal shutdown
		close(p.shutdownCh)

		// Cancel worker context
		p.cancel()

		// Wait for workers with timeout
		done := make(chan struct{})
		go func() {
			p.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			// Clean shutdown
		case <-ctx.Done():
			// Timeout occurred
			shutdownErr = fmt.Errorf("pipeline shutdown timeout: %w", ctx.Err())
		}

		// Shutdown all exporters
		for _, exporter := range p.exporters {
			if err := exporter.Shutdown(ctx); err != nil {
				if shutdownErr == nil {
					shutdownErr = fmt.Errorf("exporter %s shutdown: %w", exporter.Name(), err)
				}
			}
		}
	})

	return shutdownErr
}

// Stats returns pipeline statistics
func (p *Pipeline) Stats() PipelineStats {
	return PipelineStats{
		BufferLen:      p.buffer.Len(),
		BufferCap:      p.buffer.Cap(),
		BufferDropped:  p.buffer.Dropped(),
		Processed:      p.processed.Load(),
		Dropped:        p.dropped.Load(),
		Errors:         p.errors.Load(),
		MemoryUsage:    p.memUsage.Load(),
		MaxMemory:      p.cfg.MaxMemoryBytes,
		Workers:        p.workers,
		ExporterErrors: p.getExporterErrors(),
	}
}

func (p *Pipeline) getExporterErrors() map[string]uint64 {
	errors := make(map[string]uint64, len(p.exporters))
	for i, exp := range p.exporters {
		errors[exp.Name()] = p.exporterErrors[i].Load()
	}
	return errors
}

// PipelineStats contains statistics about the pipeline
type PipelineStats struct {
	BufferLen      int
	BufferCap      int
	BufferDropped  uint64
	Processed      uint64
	Dropped        uint64
	Errors         uint64
	MemoryUsage    int64
	MaxMemory      int64
	Workers        int
	ExporterErrors map[string]uint64
}
