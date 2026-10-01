package stats

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/convoy-road-trips-app/stats/transport"
)

func TestPipeline_DropOldest_keeps_memory_accounting_equal_to_the_queued_metrics(t *testing.T) {
	// Given: a full 4-slot buffer without workers
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := DefaultConfig()
	cfg.BufferSize = 4
	cfg.DropStrategy = DropOldest
	p := &Pipeline{
		cfg:            cfg,
		buffer:         transport.NewRingBuffer(cfg.BufferSize),
		exporterErrors: make([]atomic.Uint64, 0),
		ctx:            ctx,
		cancel:         cancel,
		shutdownCh:     make(chan struct{}),
	}

	// When: ten more metrics overflow it
	for i := range 14 {
		require.NoError(t, p.Record(ctx, &Metric{Name: "m", Type: MetricTypeCounter, Value: float64(i)}))
	}

	// Then: the four newest remain, and memory covers exactly those
	items := p.buffer.PopBatch(10)
	require.Len(t, items, 4)
	var queued int64
	for i, item := range items {
		m := item.(*Metric)
		require.InDelta(t, float64(10+i), m.Value, 0.001)
		queued += m.EstimateSize()
	}
	require.Equal(t, queued, p.memUsage.Load())
	require.EqualValues(t, 10, p.buffer.Dropped())
}
