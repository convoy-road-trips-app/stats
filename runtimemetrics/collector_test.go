package runtimemetrics

import (
	"context"
	"math"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/convoy-road-trips-app/stats/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectOnce(t *testing.T) {
	var mu sync.Mutex
	captured := map[string]models.MetricType{}

	c := New(Config{CollectInterval: time.Minute, Prefix: "runtime.go"}, func(name string, mtype models.MetricType, value float64) {
		mu.Lock()
		defer mu.Unlock()
		captured[name] = mtype
		assert.True(t, !math.IsNaN(value) && !math.IsInf(value, 0), "value must be finite: %s=%v", name, value)
		assert.GreaterOrEqual(t, value, 0.0, "value must be non-negative: %s=%v", name, value)
	})

	c.Collect()

	mu.Lock()
	defer mu.Unlock()

	for name, mtype := range captured {
		assert.Equal(t, models.MetricTypeGauge, mtype, "metric %s must be Gauge", name)
	}

	assert.Contains(t, captured, "runtime.go.memory.heap.alloc")
	assert.Contains(t, captured, "runtime.go.goroutines")
	assert.Contains(t, captured, "runtime.go.cpu.total.seconds")
}

func TestCustomPrefix(t *testing.T) {
	var mu sync.Mutex
	captured := map[string]bool{}

	c := New(Config{Prefix: "custom_prefix"}, func(name string, _ models.MetricType, _ float64) {
		mu.Lock()
		defer mu.Unlock()
		captured[name] = true
	})

	c.Collect()

	mu.Lock()
	defer mu.Unlock()

	assert.Contains(t, captured, "custom_prefix.goroutines")
	assert.Contains(t, captured, "custom_prefix.cpu.user.seconds")
}

func TestStartStop(t *testing.T) {
	before := runtime.NumGoroutine()

	c := New(Config{CollectInterval: 10 * time.Millisecond, Prefix: "test"}, func(string, models.MetricType, float64) {})
	c.Start()
	time.Sleep(30 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := c.Stop(ctx)
	require.NoError(t, err)

	time.Sleep(10 * time.Millisecond)

	after := runtime.NumGoroutine()
	assert.LessOrEqual(t, after, before+1)
}

func TestDoubleStart(t *testing.T) {
	before := runtime.NumGoroutine()

	c := New(Config{CollectInterval: 10 * time.Millisecond, Prefix: "test"}, func(string, models.MetricType, float64) {})
	c.Start()
	c.Start()

	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := c.Stop(ctx)
	require.NoError(t, err)

	time.Sleep(10 * time.Millisecond)

	after := runtime.NumGoroutine()
	assert.LessOrEqual(t, after, before+1)
}

func TestStopWithoutStart(t *testing.T) {
	c := New(Config{CollectInterval: 10 * time.Millisecond, Prefix: "test"}, func(string, models.MetricType, float64) {})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := c.Stop(ctx)
	require.NoError(t, err)
}
