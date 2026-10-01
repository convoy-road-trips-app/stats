package stats

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats/serializers"
)

func TestRecord_writes_back_attributes_in_canonical_order_so_permutations_are_one_backend_series(t *testing.T) {
	// Given: a one-series limit and the same attribute set in two orders,
	// the second with a duplicate key that the set collapses to its last value
	cfg := DefaultConfig()
	cfg.MaxCardinality = 1
	p := newUnstartedPipeline(t, cfg)
	require.NoError(t, p.Record(context.Background(), observation("requests_total",
		attribute.String("route", "/a"), attribute.String("method", "GET"))))

	// When
	err := p.Record(context.Background(), observation("requests_total",
		attribute.String("method", "POST"), attribute.String("route", "/a"), attribute.String("method", "GET")))

	// Then: both are the admitted series, and StatsD writes one metric path
	require.NoError(t, err)
	want := []attribute.KeyValue{attribute.String("method", "GET"), attribute.String("route", "/a")}
	first, second := bufferedMetric(t, p), bufferedMetric(t, p)
	require.Equal(t, want, first.Attributes)
	require.Equal(t, want, second.Attributes)
	lines, err := serializers.NewStatsDSerializer("").Serialize([]*Metric{first, second})
	require.NoError(t, err)
	require.Len(t, lines, 2)
	require.Equal(t, string(lines[0]), string(lines[1]))
}

func bufferedMetric(t *testing.T, p *Pipeline) *Metric {
	t.Helper()
	items := p.buffer.PopBatch(1)
	require.Len(t, items, 1)
	m, ok := items[0].(*Metric)
	require.True(t, ok)
	return m
}
