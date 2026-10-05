package stats

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDatadogEndpointInvalidIsErrInvalidConfig(t *testing.T) {
	for _, ep := range []string{"nope", "tcp://h:1", "unixgram://", "unixgram://rel.sock", "h:0"} {
		_, err := NewClient(WithDatadog(&DatadogConfig{Endpoint: ep}))
		require.ErrorIs(t, err, ErrInvalidConfig, ep)
	}
	_, err := NewClient(WithDatadog(&DatadogConfig{Endpoint: "h:1", BufferSize: 65508}))
	require.ErrorIs(t, err, ErrInvalidConfig)
}

func TestDatadogUnixgramMissingSocketIsDialError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unixgram is unavailable on Windows")
	}
	_, err := NewClient(WithDatadog(&DatadogConfig{Endpoint: "unixgram:///nonexistent-dir/sp.sock"}))
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrInvalidConfig)
	require.ErrorContains(t, err, "create datadog exporter")
}
