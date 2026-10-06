package stats

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithRuntimeProcessMetrics(t *testing.T) {
	cfg := DefaultConfig()
	WithRuntimeProcessMetrics()(cfg)

	require.NotNil(t, cfg.RuntimeMetrics)
	assert.True(t, cfg.RuntimeMetrics.Enabled, "implies runtime metrics")
	assert.True(t, cfg.RuntimeMetrics.ProcessMetrics)
	assert.False(t, cfg.RuntimeMetrics.DelayMetrics)
}
