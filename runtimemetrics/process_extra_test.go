package runtimemetrics

import (
	"errors"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/convoy-road-trips-app/stats/runtimemetrics/procfs"
)

func TestProcessVirtualMemory(t *testing.T) {
	rec := &recorder{}
	newProcessCollector(t, newFakeFS(t), rec, nil).Collect()

	got, ok := rec.get("runtime.go.memory.virtual.bytes", "")
	require.True(t, ok)
	assert.InDelta(t, float64(2000*os.Getpagesize()), got, 0)

	// Distinct from memory.total.bytes, which stays the host/cgroup capacity.
	total, ok := rec.get("runtime.go.memory.total.bytes", "")
	require.True(t, ok)
	assert.InDelta(t, float64(16384000*1024), total, 0)
}

func TestProcessResidentPercent(t *testing.T) {
	fs := newFakeFS(t)
	fs.files[cgroupMemoryMax] = []byte("536870912\n")
	rec := &recorder{}
	newProcessCollector(t, fs, rec, nil).Collect()

	got, ok := rec.get("runtime.go.memory.usage.percent", "resident")
	require.True(t, ok)
	assert.InDelta(t, float64(51200*1024)/536870912*100, got, 1e-9)

	// Without the cgroup cap the host total is the denominator.
	delete(fs.files, cgroupMemoryMax)
	rec.reset()
	newProcessCollector(t, fs, rec, nil).Collect()
	got, _ = rec.get("runtime.go.memory.usage.percent", "resident")
	assert.InDelta(t, float64(51200*1024)/float64(16384000*1024)*100, got, 1e-9)
}

func TestProcessResidentPercentNeedsBothSources(t *testing.T) {
	fs := newFakeFS(t)
	delete(fs.files, procMeminfoPath)
	rec := &recorder{}
	newProcessCollector(t, fs, rec, &errLog{}).Collect()
	_, ok := rec.get("runtime.go.memory.usage.percent", "resident")
	assert.False(t, ok, "no memory total, no percent")
}

func TestProcessCPUTotalAndSplitPercent(t *testing.T) {
	fs := newFakeFS(t)
	rec := &recorder{}
	c := newProcessCollector(t, fs, rec, nil)
	now := time.Unix(1000, 0)
	c.proc.now = func() time.Time { return now }

	c.Collect()
	total, ok := rec.get("runtime.go.cpu.usage_total.seconds", "")
	require.True(t, ok)
	assert.InDelta(t, 4.2, total, 1e-9, "300+120 ticks")
	for _, n := range []string{"cpu.usage_user.percent", "cpu.usage_system.percent", "cpu.usage_total.percent"} {
		_, ok := rec.get("runtime.go."+n, "")
		assert.False(t, ok, "%s needs a baseline", n)
	}

	// +60 ticks user, +40 ticks system after 2s wall.
	fs.mu.Lock()
	fs.files[procStatPath] = []byte("1234 (x) S 1 1 1 0 -1 0 0 0 0 0 360 160 0 0 20 0 8 0 5000 0 0")
	fs.mu.Unlock()
	now = now.Add(2 * time.Second)
	rec.reset()
	c.Collect()

	gomax := float64(runtime.GOMAXPROCS(0))
	for name, want := range map[string]float64{
		"cpu.usage_user.percent":   0.6 / 2 / gomax * 100,
		"cpu.usage_system.percent": 0.4 / 2 / gomax * 100,
		"cpu.usage_total.percent":  1.0 / 2 / gomax * 100,
		"cpu.usage.percent":        1.0 / 2 / gomax * 100,
	} {
		got, ok := rec.get("runtime.go."+name, "")
		require.True(t, ok, name)
		assert.InDelta(t, want, got, 1e-9, name)
	}
}

func TestProcessCPUPercentUsesCgroupQuota(t *testing.T) {
	fs := newFakeFS(t)
	fs.cpu = procfs.CPUConfig{Quota: 150 * time.Millisecond, Period: 100 * time.Millisecond, Weight: 200}
	rec := &recorder{}
	c := newProcessCollector(t, fs, rec, nil)
	now := time.Unix(1000, 0)
	c.proc.now = func() time.Time { return now }

	c.Collect()
	fs.mu.Lock()
	fs.files[procStatPath] = []byte("1234 (x) S 1 1 1 0 -1 0 0 0 0 0 400 120 0 0 20 0 8 0 5000 0 0")
	fs.mu.Unlock()
	now = now.Add(2 * time.Second)
	rec.reset()
	c.Collect()

	got, ok := rec.get("runtime.go.cpu.usage_total.percent", "")
	require.True(t, ok)
	assert.InDelta(t, 1.0/2/1.5*100, got, 1e-9, "1 CPU-second over 2s against a 1.5 core quota")

	// The pre-existing series keeps its GOMAXPROCS denominator.
	old, _ := rec.get("runtime.go.cpu.usage.percent", "")
	assert.InDelta(t, 1.0/2/float64(runtime.GOMAXPROCS(0))*100, old, 1e-9)
}

func TestProcessCgroupCPUGauges(t *testing.T) {
	t.Run("v2 quota and weight", func(t *testing.T) {
		fs := newFakeFS(t)
		fs.cpu = procfs.CPUConfig{Quota: 150 * time.Millisecond, Period: 100 * time.Millisecond, Weight: 200}
		rec := &recorder{}
		newProcessCollector(t, fs, rec, nil).Collect()

		for name, want := range map[string]float64{
			"cpu.cgroup.quota.seconds":  0.15,
			"cpu.cgroup.period.seconds": 0.1,
			"cpu.cgroup.weight":         200,
		} {
			got, ok := rec.get("runtime.go."+name, "")
			require.True(t, ok, name)
			assert.InDelta(t, want, got, 1e-9, name)
		}
		_, ok := rec.get("runtime.go.cpu.cgroup.shares", "")
		assert.False(t, ok)
	})

	t.Run("v1 unlimited quota with shares", func(t *testing.T) {
		fs := newFakeFS(t)
		fs.cpu = procfs.CPUConfig{Period: 100 * time.Millisecond, Shares: 512}
		rec := &recorder{}
		newProcessCollector(t, fs, rec, nil).Collect()

		shares, ok := rec.get("runtime.go.cpu.cgroup.shares", "")
		require.True(t, ok)
		assert.InDelta(t, 512, shares, 0)
		_, ok = rec.get("runtime.go.cpu.cgroup.quota.seconds", "")
		assert.False(t, ok, "no quota series when unlimited")
		_, ok = rec.get("runtime.go.cpu.cgroup.weight", "")
		assert.False(t, ok)
	})

	t.Run("no cgroup is not an error", func(t *testing.T) {
		fs := newFakeFS(t)
		fs.cpuErr = procfs.ErrNoCPUCgroup
		el := &errLog{}
		rec := &recorder{}
		newProcessCollector(t, fs, rec, el).Collect()
		assert.Empty(t, el.calls)
		assert.False(t, rec.hasPrefix("runtime.go.cpu.cgroup"))
	})

	t.Run("read failure reported once", func(t *testing.T) {
		fs := newFakeFS(t)
		fs.cpuErr = errors.New("denied")
		el := &errLog{}
		c := newProcessCollector(t, fs, &recorder{}, el)
		c.Collect()
		c.Collect()
		require.Len(t, el.calls, 1)
		assert.Contains(t, el.calls[0], "cgroup.cpu")
	})
}

func TestProcessStatmErrorReportedOnce(t *testing.T) {
	fs := newFakeFS(t)
	fs.files[procStatmPath] = []byte("1 2")
	el := &errLog{}
	rec := &recorder{}
	c := newProcessCollector(t, fs, rec, el)
	c.Collect()
	c.Collect()
	assert.Len(t, el.calls, 1)
	assert.False(t, rec.hasPrefix("runtime.go.memory.virtual"))
}
