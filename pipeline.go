package stats

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

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

	// Shutdown coordination. closeMu orders closing shutdownCh after every
	// Record that already passed its shutdown check has finished enqueueing.
	shutdownOnce sync.Once
	shutdownCh   chan struct{}
	closeMu      sync.RWMutex
	flushGen     atomic.Uint64 // incremented by every Flush and Shutdown

	// flushes holds one flush channel per worker started by Start
	flushes []chan flushRequest

	// Metrics
	processed   atomic.Uint64
	dropped     atomic.Uint64
	rateLimited atomic.Uint64 // Metrics dropped due to rate limiting
	errors      atomic.Uint64
	memUsage    atomic.Int64

	// Rate limiter for backpressure control
	rateLimiter *RateLimiter

	// Per-exporter error counters (index corresponds to exporters slice)
	exporterErrors []atomic.Uint64

	// Tag validation and per-metric series limits (MaxCardinality)
	cardinality cardinalityLimiter
}

// Exporter is the interface for backend exporters
type Exporter interface {
	Name() string
	Export(ctx context.Context, metrics []*Metric) error
	Shutdown(ctx context.Context) error
}

// Start starts the worker pool
func (p *Pipeline) Start() error {
	p.flushes = make([]chan flushRequest, p.workers)
	for i := range p.flushes {
		p.flushes[i] = make(chan flushRequest)
		p.wg.Add(1)
		go p.worker(i)
	}

	return nil
}

// Record adds a metric to the pipeline (non-blocking)
func (p *Pipeline) Record(ctx context.Context, m *Metric) error {
	p.closeMu.RLock()
	defer p.closeMu.RUnlock()
	select {
	case <-p.shutdownCh:
		return ErrClientClosed
	default:
	}

	// Check rate limit if enabled
	if p.rateLimiter != nil && !p.rateLimiter.Allow() {
		p.rateLimited.Add(1)
		return ErrRateLimitExceeded
	}

	// Set timestamp if not set
	if m.Timestamp.IsZero() {
		m.Timestamp = time.Now()
	}
	attachExemplar(ctx, m)

	return p.admitAndEnqueue(m)
}

// enqueue reserves memory and pushes m to the buffer according to DropStrategy.
func (p *Pipeline) enqueue(m *Metric) error {
	// Atomically reserve memory using CAS loop to prevent race condition
	// This fixes the TOCTOU (time-of-check-time-of-use) race
	size := m.EstimateSize()
	for {
		current := p.memUsage.Load()
		newUsage := current + size

		// Check if adding this metric would exceed memory limit
		if newUsage > p.cfg.MaxMemoryBytes {
			p.dropped.Add(1)
			return ErrMemoryLimit
		}

		// Atomically reserve memory
		if p.memUsage.CompareAndSwap(current, newUsage) {
			// Successfully reserved memory, now try to push to buffer
			break
		}
		// CAS failed, retry the loop
	}

	// Try to push to buffer (non-blocking)
	if p.cfg.DropStrategy == DropOldest {
		return p.enqueueDropOldest(m, size)
	}
	if !p.buffer.Push(m) {
		// Buffer is full - rollback memory reservation and drop the new metric
		p.memUsage.Add(-size)
		p.dropped.Add(1)
		return ErrBufferFull
	}

	// Successfully pushed to buffer with memory reserved
	return nil
}

// enqueueDropOldest publishes m, evicting the oldest buffered metric when the
// buffer is full. Eviction and publication are one buffer step, so a metric is
// evicted only if m takes its place: when the buffer cannot take m without
// waiting on another goroutine, m is dropped and the queue is left intact.
// The caller has reserved size bytes for m.
func (p *Pipeline) enqueueDropOldest(m *Metric, size int64) error {
	evicted, ok := p.buffer.PushDropOldest(m)
	if !ok {
		p.memUsage.Add(-size)
		p.dropped.Add(1)
		return ErrBufferFull
	}
	if old, isMetric := evicted.(*Metric); isMetric {
		p.memUsage.Add(-old.EstimateSize())
		ReleaseMetric(old)
	}
	return nil
}

// worker processes metrics from the buffer. Flush and Shutdown reach it through
// its flush channel; the channel is nil for a worker started outside Start.
func (p *Pipeline) worker(id int) {
	defer p.wg.Done()

	var flushes <-chan flushRequest
	if id < len(p.flushes) {
		flushes = p.flushes[id]
	}

	var inFlight exportFailure
	// Batch buffer for efficient processing
	batch := make([]*Metric, 0, 100)
	ticker := time.NewTicker(p.cfg.FlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			// Shutdown ended before this worker drained; exporters may already be
			// shut down, so the remaining batch is dropped rather than exported late.
			p.dropped.Add(uint64(len(batch)))
			for _, m := range batch {
				ReleaseMetric(m)
			}
			return

		case request := <-flushes:
			request.done <- errors.Join(inFlight.since(request.gen), p.drain(request.ctx, batch))
			batch = batch[:0]
			if request.stop {
				return
			}

		case <-ticker.C:
			// Flush on timer
			batch = p.cardinality.appendDropCounters(batch)
			if len(batch) > 0 {
				inFlight.record(p.exportInBackground(batch), &p.flushGen)
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
			batch = p.appendPopped(batch, items)

			// Flush if batch is full
			if len(batch) >= cap(batch) {
				inFlight.record(p.exportInBackground(batch), &p.flushGen)
				batch = batch[:0]
			}
		}
	}
}

// appendPopped appends ring items to batch and releases their memory reservation.
func (p *Pipeline) appendPopped(batch []*Metric, items []any) []*Metric {
	for _, item := range items {
		if m, ok := item.(*Metric); ok {
			batch = append(batch, m)
			p.memUsage.Add(-m.EstimateSize())
		}
	}
	return batch
}

// exportInBackground exports a batch the worker loop collected on its own.
// Failures are counted in Stats and reported by an overlapping Flush.
//
// The UDPTimeout deadline applies to exporters that do not bound themselves.
// An exporter implementing exportTimeouter (OTLP) is bounded by its own timeout
// instead: a 100ms UDP write deadline cancels every real network round trip.
func (p *Pipeline) exportInBackground(batch []*Metric) error {
	return p.processBatchWith(p.ctx, batch, p.cfg.UDPTimeout)
}

// exportTimeouter is implemented by exporters that bound their own exports
// and must not inherit the pipeline's UDP write deadline.
type exportTimeouter interface {
	ExportTimeout() time.Duration
}

// processBatch sends a batch of metrics to all exporters with ctx and returns
// their joined errors. The metrics are returned to the pool afterwards.
func (p *Pipeline) processBatch(ctx context.Context, batch []*Metric) error {
	return p.processBatchWith(ctx, batch, 0)
}

// processBatchWith is processBatch with a default per-export timeout (0 for
// none) for exporters that do not implement exportTimeouter.
func (p *Pipeline) processBatchWith(ctx context.Context, batch []*Metric, defaultTimeout time.Duration) error {
	if len(batch) == 0 {
		return nil
	}

	// Send to each exporter in parallel
	var wg sync.WaitGroup
	errs := make([]error, len(p.exporters))

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
					errs[idx] = fmt.Errorf("exporter %s panicked: %v", exp.Name(), r)
				}
			}()

			exportCtx := ctx
			if _, bounded := exp.(exportTimeouter); !bounded && defaultTimeout > 0 {
				var cancel context.CancelFunc
				exportCtx, cancel = context.WithTimeout(ctx, defaultTimeout)
				defer cancel()
			}
			if err := exp.Export(exportCtx, batch); err != nil {
				p.errors.Add(1)
				p.exporterErrors[idx].Add(1)
				errs[idx] = fmt.Errorf("exporter %s: %w", exp.Name(), err)
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
	return errors.Join(errs...)
}

// Stats returns pipeline statistics
func (p *Pipeline) Stats() PipelineStats {
	stats := PipelineStats{
		BufferLen:      p.buffer.Len(),
		BufferCap:      p.buffer.Cap(),
		BufferDropped:  p.buffer.Dropped(),
		Processed:      p.processed.Load(),
		Dropped:        p.dropped.Load(),
		RateLimited:    p.rateLimited.Load(),
		Errors:         p.errors.Load(),
		MemoryUsage:    p.memUsage.Load(),
		MaxMemory:      p.cfg.MaxMemoryBytes,
		Workers:        p.workers,
		ExporterErrors: p.getExporterErrors(),
		DroppedLabels:  p.cardinality.total.Load(),
	}

	// Add rate limiter stats if enabled
	if p.rateLimiter != nil {
		rlStats := p.rateLimiter.Stats()
		stats.RateLimiter = &rlStats
	}

	return stats
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
	RateLimited    uint64 // Metrics dropped due to rate limiting
	Errors         uint64
	MemoryUsage    int64
	MaxMemory      int64
	Workers        int
	ExporterErrors map[string]uint64
	RateLimiter    *RateLimiterStats // Rate limiter stats (nil if disabled)
	DroppedLabels  uint64            // Labels and series dropped by tag validation and cardinality limits
}
