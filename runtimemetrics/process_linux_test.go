//go:build linux

package runtimemetrics

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessRealSources(t *testing.T) {
	rec := &recorder{}
	el := &errLog{}
	c := New(Config{Prefix: "runtime.go", ProcessMetrics: true, OnError: el.onError}, rec.record)
	c.Collect()

	assert.Empty(t, el.calls)
	n, ok := rec.get("runtime.go.threads.count", "")
	require.True(t, ok)
	assert.GreaterOrEqual(t, n, 1.0)
	fds, ok := rec.get("runtime.go.files.open.count", "")
	require.True(t, ok)
	assert.GreaterOrEqual(t, fds, 3.0)
	_, ok = rec.get("runtime.go.cpu.usage.seconds", "user")
	assert.True(t, ok)
}
