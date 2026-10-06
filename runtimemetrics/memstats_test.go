package runtimemetrics

import (
	"math"
	"runtime"
	"runtime/metrics"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats/models"
)

type recorded struct {
	mu   sync.Mutex
	vals map[string]float64
}

func newRecorded() *recorded { return &recorded{vals: map[string]float64{}} }

func (r *recorded) fn(name string, _ models.MetricType, v float64, _ ...attribute.KeyValue) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.vals[name] = v
}

func (r *recorded) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.vals = map[string]float64{}
}

func hist(counts []uint64, buckets []float64) *metrics.Float64Histogram {
	return &metrics.Float64Histogram{Counts: counts, Buckets: buckets}
}

func TestNewMemstatsNamesPresent(t *testing.T) {
	rec := newRecorded()
	c := New(Config{Prefix: "rt"}, rec.fn)
	c.Collect()

	have := map[string]bool{}
	for _, d := range metrics.All() {
		have[d.Name] = true
	}
	require.True(t, have["/memory/classes/metadata/mspan/inuse:bytes"], "test assumes modern runtime/metrics names")

	for _, n := range []string{
		"memory.stack.sys", "memory.mspan.inuse", "memory.mspan.sys",
		"memory.mcache.inuse", "memory.mcache.sys", "memory.buckhash.sys",
		"memory.gc.sys", "memory.other.sys", "gc.next.bytes", "gc.cpu.fraction",
	} {
		v, ok := rec.vals["rt."+n]
		assert.True(t, ok, "missing %s", n)
		assert.True(t, isFinite(v) && v >= 0, "%s must be finite and non-negative: %v", n, v)
	}
	assert.InDelta(t, rec.vals["rt.heap.goal.bytes"], rec.vals["rt.gc.next.bytes"], 0)
	assert.LessOrEqual(t, rec.vals["rt.gc.cpu.fraction"], 1.0)
	assert.GreaterOrEqual(t, rec.vals["rt.memory.mspan.sys"], rec.vals["rt.memory.mspan.inuse"])
	assert.GreaterOrEqual(t, rec.vals["rt.memory.stack.sys"], rec.vals["rt.memory.stack.inuse"])

	for name, v := range rec.vals {
		assert.False(t, math.IsNaN(v), "%s is NaN", name)
	}
}

func TestExistingNamesUnchanged(t *testing.T) {
	golden := []string{
		"memory.heap.alloc", "memory.heap.inuse", "memory.heap.idle", "memory.heap.released",
		"memory.sys", "memory.stack.inuse", "heap.allocs.bytes", "heap.frees.bytes",
		"heap.allocs.objects", "heap.frees.objects", "heap.objects.live", "heap.goal.bytes",
		"gc.cycles.total", "gc.cpu.seconds", "cpu.gc.seconds", "goroutines", "cgo.calls",
		"cpu.total.seconds", "cpu.user.seconds", "cpu.idle.seconds", "cpu.scavenge.seconds",
	}

	// Mapping table: every golden name is still produced from the same source.
	got := map[string]string{}
	for _, m := range getMappings() {
		for _, n := range m.metricNames {
			got[n] = m.runtimeName
		}
	}
	for _, d := range getDerivedMappings() {
		got[d.metricName] = "derived"
	}
	for _, n := range golden {
		assert.Contains(t, got, n)
	}
	assert.Equal(t, "/memory/classes/heap/stacks:bytes", got["memory.stack.inuse"])
	assert.Equal(t, "/gc/heap/goal:bytes", got["heap.goal.bytes"])
	assert.Equal(t, "/cpu/classes/gc/total:cpu-seconds", got["gc.cpu.seconds"])
	assert.Equal(t, "/cpu/classes/gc/total:cpu-seconds", got["cpu.gc.seconds"])

	// Emitted names at runtime.
	// Names whose source is absent on the running Go version are skipped.
	known := map[string]bool{}
	for _, d := range metrics.All() {
		known[d.Name] = true
	}
	rec := newRecorded()
	New(Config{Prefix: "rt"}, rec.fn).Collect()
	assert.Contains(t, rec.vals, "rt.gomaxprocs")
	for _, n := range golden {
		if known[got[n]] || got[n] == "derived" {
			assert.Contains(t, rec.vals, "rt."+n)
		}
	}
}

func TestGCPauseStatsFromHistogramDelta(t *testing.T) {
	buckets := []float64{0, 1, 2, 4, 8}
	rec := newRecorded()
	c := New(Config{Prefix: "rt"}, rec.fn)

	// First snapshot: baseline is zero, so the full histogram counts.
	c.recordPauseStats(hist([]uint64{2, 0, 0, 0}, buckets))
	assert.InDelta(t, 0.0, rec.vals["rt.gc.pause.seconds.min"], 1e-12)
	assert.InDelta(t, 1.0, rec.vals["rt.gc.pause.seconds.max"], 1e-12)
	assert.InDelta(t, 0.5, rec.vals["rt.gc.pause.seconds.avg"], 1e-12)

	// Second snapshot: delta = {0, 1, 0, 3}; avg = (1*1.5 + 3*6)/4.
	rec.reset()
	c.recordPauseStats(hist([]uint64{2, 1, 0, 3}, buckets))
	assert.InDelta(t, 1.0, rec.vals["rt.gc.pause.seconds.min"], 1e-12)
	assert.InDelta(t, 8.0, rec.vals["rt.gc.pause.seconds.max"], 1e-12)
	assert.InDelta(t, (1.5+18.0)/4.0, rec.vals["rt.gc.pause.seconds.avg"], 1e-12)

	// Third snapshot is identical: zero delta, nothing emitted.
	rec.reset()
	c.recordPauseStats(hist([]uint64{2, 1, 0, 3}, buckets))
	assert.Empty(t, rec.vals)
}

func TestGCPauseStatsSkippedWithoutPauses(t *testing.T) {
	buckets := []float64{0, 1, 2}
	rec := newRecorded()
	c := New(Config{Prefix: "rt"}, rec.fn)

	// Before any GC: all counts zero.
	c.recordPauseStats(hist([]uint64{0, 0}, buckets))
	assert.Empty(t, rec.vals)

	// Nil and malformed histograms are ignored.
	c.recordPauseStats(nil)
	c.recordPauseStats(hist([]uint64{1}, buckets))
	assert.Empty(t, rec.vals)
}

func TestGCPauseStatsInfiniteBounds(t *testing.T) {
	rec := newRecorded()
	c := New(Config{Prefix: "rt"}, rec.fn)
	c.recordPauseStats(hist([]uint64{1, 0, 1}, []float64{math.Inf(-1), 1, 2, math.Inf(1)}))

	assert.InDelta(t, 1.0, rec.vals["rt.gc.pause.seconds.min"], 1e-12)
	assert.InDelta(t, 2.0, rec.vals["rt.gc.pause.seconds.max"], 1e-12)
	for name, v := range rec.vals {
		assert.True(t, isFinite(v), "%s must be finite", name)
	}
}

func TestGCPauseStatsCountsDeepCopied(t *testing.T) {
	buckets := []float64{0, 1, 2}
	rec := newRecorded()
	c := New(Config{Prefix: "rt"}, rec.fn)

	counts := []uint64{3, 0}
	c.recordPauseStats(hist(counts, buckets))

	// The runtime may reuse the slice; mutating it must not change the baseline.
	counts[0] = 100
	rec.reset()
	c.recordPauseStats(hist([]uint64{3, 2}, buckets))
	assert.InDelta(t, 1.0, rec.vals["rt.gc.pause.seconds.min"], 1e-12)
	assert.InDelta(t, 2.0, rec.vals["rt.gc.pause.seconds.max"], 1e-12)
	assert.InDelta(t, 1.5, rec.vals["rt.gc.pause.seconds.avg"], 1e-12)

	// A decreasing counter (reset) is skipped rather than underflowing.
	rec.reset()
	c.recordPauseStats(hist([]uint64{1, 0}, buckets))
	assert.Empty(t, rec.vals)
}

func TestGCPauseStatsLiveCollect(t *testing.T) {
	rec := newRecorded()
	c := New(Config{Prefix: "rt"}, rec.fn)
	c.Collect()
	rec.reset()

	runtime.GC()
	c.Collect()

	minV, okMin := rec.vals["rt.gc.pause.seconds.min"]
	maxV, okMax := rec.vals["rt.gc.pause.seconds.max"]
	avg, okAvg := rec.vals["rt.gc.pause.seconds.avg"]
	require.True(t, okMin && okMax && okAvg, "forced GC must produce pause stats")
	assert.LessOrEqual(t, minV, maxV)
	assert.True(t, isFinite(avg))
}

func TestMissingSourceSkipsDerived(t *testing.T) {
	rec := newRecorded()
	c := New(Config{Prefix: "rt"}, rec.fn)
	// Simulate a Go version that lacks a source metric.
	delete(c.sampleIdx, "/memory/classes/metadata/mspan/free:bytes")
	c.Collect()

	assert.NotContains(t, rec.vals, "rt.memory.mspan.sys")
	assert.Contains(t, rec.vals, "rt.memory.mspan.inuse")
}
