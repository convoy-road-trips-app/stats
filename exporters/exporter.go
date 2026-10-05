package exporters

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

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

// NewBaseExporter creates a new base exporter that sends over UDP
func NewBaseExporter(name, address string, serializer Serializer) (*BaseExporter, error) {
	return NewBaseExporterNetwork(name, "udp", address, serializer)
}

// NewBaseExporterNetwork creates a new base exporter that sends over the given
// network ("udp" or "unixgram"; see transport.NewPool).
func NewBaseExporterNetwork(name, network, address string, serializer Serializer) (*BaseExporter, error) {
	pool, err := transport.NewPool(network, address, 4, 100*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("create %s pool: %w", network, err)
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

	return e.send(ctx, packets, len(metrics))
}

// SendPackets sends already serialized datagrams through the exporter's pool
// and circuit breaker. count is the number of metrics the packets carry, for
// the exported statistic.
func (e *BaseExporter) SendPackets(ctx context.Context, packets [][]byte, count int) error {
	if len(packets) == 0 {
		return nil
	}
	return e.breaker.Call(ctx, func() error {
		return e.send(ctx, packets, count)
	})
}

func (e *BaseExporter) send(ctx context.Context, packets [][]byte, count int) error {
	if err := e.pool.SendBatch(ctx, packets); err != nil {
		e.errors.Add(1)
		return fmt.Errorf("send batch: %w", err)
	}

	if count > 0 {
		e.exported.Add(uint64(count))
	}
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
