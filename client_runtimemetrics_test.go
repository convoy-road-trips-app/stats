package stats

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientRuntimeMetrics_Enabled(t *testing.T) {
	client, err := NewClient(
		WithServiceName("test-runtime"),
		WithRuntimeMetrics(),
	)
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	s := client.Stats()
	assert.Greater(t, s.Pipeline.Processed, uint64(0), "expected runtime metrics to be processed by pipeline")
	require.NoError(t, client.Close())
}

func TestClientRuntimeMetrics_DisabledByDefault(t *testing.T) {
	client, err := NewClient(WithServiceName("test-no-runtime"))
	require.NoError(t, err)

	assert.Nil(t, client.collector, "collector should be nil when runtime metrics are not enabled")

	time.Sleep(50 * time.Millisecond)
	require.NoError(t, client.Close())
}

func TestClientRuntimeMetrics_Close_NoDeadlock(t *testing.T) {
	client, err := NewClient(
		WithServiceName("test-close"),
		WithRuntimeMetrics(),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = client.Shutdown(ctx)
	require.NoError(t, err)
}
