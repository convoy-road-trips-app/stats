package stats

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWithExporterReceivesBatch(t *testing.T) {
	var mu sync.Mutex
	var got []string
	mock := &MockExporter{name: "custom", exportFunc: func(_ context.Context, ms []*Metric) error {
		mu.Lock()
		defer mu.Unlock()
		for _, m := range ms {
			got = append(got, m.Name)
		}
		return nil
	}}

	client, err := NewClient(WithFlushInterval(time.Hour), WithExporter(mock))
	require.NoError(t, err)
	defer client.Close()

	require.NoError(t, client.Counter(context.Background(), "custom.hits", 1))
	require.NoError(t, client.Flush(context.Background()))

	mu.Lock()
	defer mu.Unlock()
	require.Contains(t, got, "custom.hits")
	_, ok := client.Stats().Pipeline.ExporterErrors["custom"]
	require.True(t, ok, "custom exporter gets an ExporterErrors slot")
}

func TestCustomExporterPanicIsRecovered(t *testing.T) {
	var calls int
	var mu sync.Mutex
	mock := &MockExporter{name: "panicky", exportFunc: func(context.Context, []*Metric) error {
		mu.Lock()
		defer mu.Unlock()
		calls++
		panic("boom")
	}}

	client, err := NewClient(WithFlushInterval(time.Hour), WithExporter(mock))
	require.NoError(t, err)
	defer client.Close()

	ctx := context.Background()
	require.NoError(t, client.Counter(ctx, "a", 1))
	_ = client.Flush(ctx)
	require.Equal(t, uint64(1), client.Stats().Pipeline.ExporterErrors["panicky"])

	// The client keeps working after the panic.
	require.NoError(t, client.Counter(ctx, "b", 1))
	_ = client.Flush(ctx)
	require.Equal(t, uint64(2), client.Stats().Pipeline.ExporterErrors["panicky"])
	mu.Lock()
	defer mu.Unlock()
	require.GreaterOrEqual(t, calls, 2)
}

func TestWithExporterNilIsInvalidConfig(t *testing.T) {
	_, err := NewClient(WithExporter(nil))
	require.ErrorIs(t, err, ErrInvalidConfig)
}

func TestWithExporterDuplicateNameIsInvalidConfig(t *testing.T) {
	t.Run("custom and custom", func(t *testing.T) {
		_, err := NewClient(
			WithExporter(&MockExporter{name: "dup"}),
			WithExporter(&MockExporter{name: "dup"}),
		)
		require.ErrorIs(t, err, ErrInvalidConfig)
	})
	t.Run("custom and built-in", func(t *testing.T) {
		_, err := NewClient(
			WithDatadog(&DatadogConfig{AgentHost: "localhost", AgentPort: 8125}),
			WithExporter(&MockExporter{name: "datadog"}),
		)
		require.ErrorIs(t, err, ErrInvalidConfig)
	})
	t.Run("distinct names", func(t *testing.T) {
		c, err := NewClient(
			WithExporter(&MockExporter{name: "one"}),
			WithExporter(&MockExporter{name: "two"}),
		)
		require.NoError(t, err)
		require.NoError(t, c.Close())
	})
}
