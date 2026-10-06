package stats

import (
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithRuntimeDelayMetrics(t *testing.T) {
	cfg := DefaultConfig()
	WithRuntimeDelayMetrics()(cfg)

	require.NotNil(t, cfg.RuntimeMetrics)
	assert.True(t, cfg.RuntimeMetrics.Enabled, "implies runtime metrics")
	assert.True(t, cfg.RuntimeMetrics.DelayMetrics)
	assert.False(t, cfg.RuntimeMetrics.ProcessMetrics)
}

func TestClientRuntimeDelayMetrics_UnsupportedPlatformCountsOneError(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("taskstats is supported (or denied) on linux; the default reader is covered by the collector tests")
	}
	client, err := NewClient(
		WithServiceName("test-delay"),
		WithRuntimeDelayMetrics(),
		func(c *Config) { c.RuntimeMetrics.CollectInterval = 10 * time.Millisecond },
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	require.Eventually(t, func() bool {
		return client.Stats().Pipeline.ExporterErrors["runtimemetrics.delay"] > 0
	}, 2*time.Second, 5*time.Millisecond)

	// Several more collections must not add errors: delay is disabled for good.
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, uint64(1), client.Stats().Pipeline.ExporterErrors["runtimemetrics.delay"])
}
