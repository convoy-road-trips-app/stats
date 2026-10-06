package runtimemetrics

import (
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/convoy-road-trips-app/stats/models"
)

func TestProcessMetricsEmitted(t *testing.T) {
	rec := &recorder{}
	c := newProcessCollector(t, newFakeFS(t), rec, nil)
	c.Collect()

	want := []struct {
		name, typ string
		value     float64
	}{
		{"runtime.go.cpu.usage.seconds", "user", 3.0},
		{"runtime.go.cpu.usage.seconds", "system", 1.2},
		{"runtime.go.memory.usage.bytes", "resident", 51200 * 1024},
		{"runtime.go.memory.usage.bytes", "shared", (20000 + 1200) * 1024},
		{"runtime.go.memory.usage.bytes", "text", 4096 * 1024},
		{"runtime.go.memory.usage.bytes", "data", 120000 * 1024},
		{"runtime.go.memory.available.bytes", "", 8192000 * 1024},
		{"runtime.go.memory.total.bytes", "", 16384000 * 1024},
		{"runtime.go.memory.pagefault.count", "major", 42},
		{"runtime.go.memory.pagefault.count", "minor", 15000},
		{"runtime.go.files.open.count", "", 17},
		{"runtime.go.files.open.max", "", 1024},
		{"runtime.go.threads.count", "", 8},
		{"runtime.go.threads.switch.count", "voluntary", 1500},
		{"runtime.go.threads.switch.count", "involuntary", 75},
	}
	for _, w := range want {
		got, ok := rec.get(w.name, w.typ)
		require.True(t, ok, "missing %s{type=%q}", w.name, w.typ)
		assert.InDelta(t, w.value, got, 1e-6, "%s{type=%q}", w.name, w.typ)
	}

	// First sample has no baseline, so no percent.
	_, ok := rec.get("runtime.go.cpu.usage.percent", "")
	assert.False(t, ok)

	for _, o := range rec.list {
		assert.Equal(t, models.MetricTypeGauge, o.mtype, o.name)
	}
}

func TestProcessCPUPercent(t *testing.T) {
	fs := newFakeFS(t)
	rec := &recorder{}
	c := newProcessCollector(t, fs, rec, nil)

	now := time.Unix(1000, 0)
	c.proc.now = func() time.Time { return now }

	c.Collect()
	_, ok := rec.get("runtime.go.cpu.usage.percent", "")
	require.False(t, ok, "first sample must be skipped")

	// +100 ticks (1s CPU) after 2s wall.
	fs.mu.Lock()
	fs.files[procStatPath] = []byte("1234 (x) S 1 1 1 0 -1 0 0 0 0 0 400 120 0 0 20 0 8 0 5000 0 0")
	fs.mu.Unlock()
	now = now.Add(2 * time.Second)
	rec.reset()
	c.Collect()

	got, ok := rec.get("runtime.go.cpu.usage.percent", "")
	require.True(t, ok)
	want := 1.0 / 2.0 / float64(runtime.GOMAXPROCS(0)) * 100
	assert.InDelta(t, want, got, 1e-6)
}

func TestProcessCgroupCapsTotal(t *testing.T) {
	fs := newFakeFS(t)
	fs.files[cgroupMemoryMax] = []byte("536870912\n")
	rec := &recorder{}
	newProcessCollector(t, fs, rec, nil).Collect()

	got, ok := rec.get("runtime.go.memory.total.bytes", "")
	require.True(t, ok)
	assert.InDelta(t, float64(536870912), got, 0)

	// A cgroup limit above physical memory does not raise the total.
	fs.files[cgroupMemoryMax] = []byte("99999999999999\n")
	rec.reset()
	newProcessCollector(t, fs, rec, nil).Collect()
	got, _ = rec.get("runtime.go.memory.total.bytes", "")
	assert.InDelta(t, float64(16384000*1024), got, 0)
}

func TestProcessMetricsDisabledEmitsNothing(t *testing.T) {
	rec := &recorder{}
	c := New(Config{Prefix: "runtime.go"}, rec.record)
	c.proc = newProcessState(newFakeFS(t).source())
	c.Collect()

	assert.False(t, rec.hasPrefix("runtime.go.cpu.usage"))
	assert.False(t, rec.hasPrefix("runtime.go.memory.usage"))
	assert.False(t, rec.hasPrefix("runtime.go.files."))
	assert.False(t, rec.hasPrefix("runtime.go.threads."))
	assert.True(t, rec.hasPrefix("runtime.go.goroutines"), "regular metrics still emitted")
}

func TestProcessOnErrorOncePerSource(t *testing.T) {
	fs := newFakeFS(t)
	boom := errors.New("boom")
	fs.errs[procStatusPath] = boom
	fs.errs[procLimitsPath] = boom
	rec := &recorder{}
	el := &errLog{}
	c := newProcessCollector(t, fs, rec, el)

	c.Collect()
	c.Collect()
	c.Collect()

	require.Len(t, el.calls, 2, "one report per failing source: %v", el.calls)
	for _, call := range el.calls {
		assert.Contains(t, call, "process: ")
		assert.Contains(t, call, "boom")
	}

	// Failing sources are skipped; healthy ones still emit.
	assert.False(t, rec.hasPrefix("runtime.go.memory.usage"))
	_, ok := rec.get("runtime.go.files.open.max", "")
	assert.False(t, ok)
	_, ok = rec.get("runtime.go.threads.count", "")
	assert.True(t, ok)
	_, ok = rec.get("runtime.go.memory.total.bytes", "")
	assert.True(t, ok)
}

func TestProcessMalformedSourceReportedOnce(t *testing.T) {
	fs := newFakeFS(t)
	fs.files[procStatPath] = []byte("garbage")
	el := &errLog{}
	c := newProcessCollector(t, fs, &recorder{}, el)
	c.Collect()
	c.Collect()
	assert.Len(t, el.calls, 1)
}

func TestProcessFDCountError(t *testing.T) {
	fs := newFakeFS(t)
	fs.errs[procFDDir] = errors.New("denied")
	el := &errLog{}
	rec := &recorder{}
	c := newProcessCollector(t, fs, rec, el)
	c.Collect()
	c.Collect()
	assert.Len(t, el.calls, 1)
	_, ok := rec.get("runtime.go.files.open.count", "")
	assert.False(t, ok)
}

func TestProcessMissingCgroupIsNotAnError(t *testing.T) {
	fs := newFakeFS(t)
	delete(fs.files, cgroupMemoryMax)
	el := &errLog{}
	rec := &recorder{}
	newProcessCollector(t, fs, rec, el).Collect()
	assert.Empty(t, el.calls)
	_, ok := rec.get("runtime.go.memory.total.bytes", "")
	assert.True(t, ok)
}
