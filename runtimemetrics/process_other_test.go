//go:build !linux && !darwin

package runtimemetrics

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProcessMetricsNoopOffLinux(t *testing.T) {
	rec := &recorder{}
	el := &errLog{}
	c := New(Config{Prefix: "runtime.go", ProcessMetrics: true, OnError: el.onError}, rec.record)
	c.Collect()

	assert.Nil(t, c.proc)
	assert.Empty(t, el.calls)
	assert.False(t, rec.hasPrefix("runtime.go.cpu.usage"))
	assert.False(t, rec.hasPrefix("runtime.go.threads."))
}
