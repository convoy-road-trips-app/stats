package exporters

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/convoy-road-trips-app/stats/models"
	"github.com/convoy-road-trips-app/stats/transport"
)

// BaseExporter provides common functionality for all exporters
type BaseExporter struct {
	name       string
	pool       *transport.UDPConnPool
	breaker    *transport.CircuitBreaker
	serializer Serializer

	// Metrics
	exported atomic.Uint64
	errors   atomic.Uint64
}

// Serializer is the interface for metric serialization
type Serializer interface {
	Serialize(metrics []*models.Metric) ([][]byte, error)
	Name() string
}

// NewBaseExporter creates a new base exporter
func NewBaseExporter(name string, address string, serializer Serializer) (*BaseExporter, error) {
	pool, err := transport.NewUDPConnPool(address, 4)
	if err != nil {
		return nil, fmt.Errorf("create UDP pool: %w", err)
	}

	breaker := transport.NewCircuitBreaker(5, 10)

	return &BaseExporter{
		name:       name,
		pool:       pool,
		breaker:    breaker,
		serializer: serializer,
	}, nil
}

// Name returns the exporter name
func (e *BaseExporter) Name() string {
	return e.name
}

// Export sends metrics to the backend
func (e *BaseExporter) Export(ctx context.Context, metrics []*models.Metric) error {
	if len(metrics) == 0 {
		return nil
	}

	// Use circuit breaker
	return e.breaker.Call(ctx, func() error {
		return e.doExport(ctx, metrics)
	})
}

// doExport performs the actual export
func (e *BaseExporter) doExport(ctx context.Context, metrics []*models.Metric) error {
	// Serialize metrics
	packets, err := e.serializer.Serialize(metrics)
	if err != nil {
		e.errors.Add(1)
		return fmt.Errorf("serialize metrics: %w", err)
	}

	// Send via UDP
	if err := e.pool.SendBatch(ctx, packets); err != nil {
		e.errors.Add(1)
		return fmt.Errorf("send batch: %w", err)
	}

	e.exported.Add(uint64(len(metrics)))
	return nil
}

// Shutdown closes the exporter
func (e *BaseExporter) Shutdown(ctx context.Context) error {
	return e.pool.Close()
}

// Stats returns exporter statistics
func (e *BaseExporter) Stats() ExporterStats {
	poolStats := e.pool.Stats()
	breakerStats := e.breaker.Stats()

	return ExporterStats{
		Name:         e.name,
		Exported:     e.exported.Load(),
		Errors:       e.errors.Load(),
		PoolStats:    poolStats,
		BreakerStats: breakerStats,
	}
}

// ExporterStats contains statistics about an exporter
type ExporterStats struct {
	Name         string
	Exported     uint64
	Errors       uint64
	PoolStats    transport.PoolStats
	BreakerStats transport.CircuitStats
}
