package runtimemetrics

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCPUInfoPhysical(t *testing.T) {
	t.Run("hyperthreaded two sockets", func(t *testing.T) {
		n, ok := parseCPUInfoPhysical(fixture(t, "proc_cpuinfo_ht.txt"))
		require.True(t, ok)
		assert.Equal(t, 2, n)
	})

	t.Run("no topology fields", func(t *testing.T) {
		_, ok := parseCPUInfoPhysical(fixture(t, "proc_cpuinfo_arm.txt"))
		assert.False(t, ok)
	})

	t.Run("empty", func(t *testing.T) {
		_, ok := parseCPUInfoPhysical(nil)
		assert.False(t, ok)
	})
}

func TestGoCPUCountMetrics(t *testing.T) {
	rec := newRecorded()
	New(Config{Prefix: "rt"}, rec.fn).Collect()

	assert.InDelta(t, float64(runtime.NumCPU()), rec.vals["rt.cpu.num"], 0)
	if v, ok := rec.vals["rt.cpu.physical.num"]; ok {
		assert.GreaterOrEqual(t, v, 1.0)
	}
}

func TestGoMemoryDistinctSeries(t *testing.T) {
	rec := newRecorded()
	New(Config{Prefix: "rt"}, rec.fn).Collect()

	alloc, ok := rec.vals["rt.memory.alloc"]
	require.True(t, ok, "memory.alloc")
	assert.InDelta(t, rec.vals["rt.memory.heap.alloc"], alloc, 0)

	heapSys, ok := rec.vals["rt.memory.heap.sys"]
	require.True(t, ok, "memory.heap.sys")
	assert.GreaterOrEqual(t, heapSys, alloc)
	assert.LessOrEqual(t, heapSys, rec.vals["rt.memory.sys"])

	// MemStats.Lookups is never written by the runtime and has no
	// runtime/metrics source, so no series is emitted.
	for name := range rec.vals {
		assert.NotContains(t, name, "lookups")
	}
}
