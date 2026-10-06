//go:build darwin

package runtimemetrics

import (
	"errors"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/convoy-road-trips-app/stats/models"
)

func darwinCollector(read func(*syscall.Rusage) error, rec *recorder, el *errLog) *Collector {
	cfg := Config{Prefix: "runtime.go", ProcessMetrics: true}
	if el != nil {
		cfg.OnError = el.onError
	}
	c := New(cfg, rec.record)
	c.proc = newDarwinProcessState(read)
	return c
}

func fakeRusage(user, system time.Duration) func(*syscall.Rusage) error {
	return func(ru *syscall.Rusage) error {
		*ru = syscall.Rusage{}
		ru.Utime = syscall.NsecToTimeval(user.Nanoseconds())
		ru.Stime = syscall.NsecToTimeval(system.Nanoseconds())
		ru.Maxrss = 52428800 // bytes on Darwin
		ru.Majflt = 7
		ru.Minflt = 900
		ru.Nvcsw = 120
		ru.Nivcsw = 15
		return nil
	}
}

func TestDarwinProcessRealRusage(t *testing.T) {
	rec := &recorder{}
	el := &errLog{}
	c := New(Config{Prefix: "runtime.go", ProcessMetrics: true, OnError: el.onError}, rec.record)
	require.NotNil(t, c.proc)

	// Burn CPU so user+system is measurably above zero.
	deadline := time.Now().Add(50 * time.Millisecond)
	x := 0
	for time.Now().Before(deadline) {
		x++
	}
	_ = x
	c.Collect()

	assert.Empty(t, el.calls)
	user, ok := rec.get("runtime.go.cpu.usage.seconds", "user")
	require.True(t, ok)
	system, ok := rec.get("runtime.go.cpu.usage.seconds", "system")
	require.True(t, ok)
	assert.GreaterOrEqual(t, user, 0.0)
	assert.GreaterOrEqual(t, system, 0.0)
	assert.Greater(t, user+system, 0.0)

	for _, k := range []struct{ name, typ string }{
		{"runtime.go.memory.usage.bytes", "resident"},
		{"runtime.go.memory.pagefault.count", "major"},
		{"runtime.go.memory.pagefault.count", "minor"},
		{"runtime.go.threads.switch.count", "voluntary"},
		{"runtime.go.threads.switch.count", "involuntary"},
	} {
		v, ok := rec.get(k.name, k.typ)
		require.True(t, ok, "missing %s{type=%q}", k.name, k.typ)
		assert.GreaterOrEqual(t, v, 0.0, k.name)
	}
	rss, _ := rec.get("runtime.go.memory.usage.bytes", "resident")
	assert.Greater(t, rss, 1024.0*1024, "ru_maxrss is bytes on Darwin")
}

func TestDarwinProcessMetricsEmitted(t *testing.T) {
	rec := &recorder{}
	darwinCollector(fakeRusage(3*time.Second, 1200*time.Millisecond), rec, nil).Collect()

	want := []struct {
		name, typ string
		value     float64
	}{
		{"runtime.go.cpu.usage.seconds", "user", 3.0},
		{"runtime.go.cpu.usage.seconds", "system", 1.2},
		{"runtime.go.memory.usage.bytes", "resident", 52428800},
		{"runtime.go.memory.pagefault.count", "major", 7},
		{"runtime.go.memory.pagefault.count", "minor", 900},
		{"runtime.go.threads.switch.count", "voluntary", 120},
		{"runtime.go.threads.switch.count", "involuntary", 15},
	}
	for _, w := range want {
		got, ok := rec.get(w.name, w.typ)
		require.True(t, ok, "missing %s{type=%q}", w.name, w.typ)
		assert.InDelta(t, w.value, got, 1e-6, "%s{type=%q}", w.name, w.typ)
	}

	// rusage does not provide these.
	for _, p := range []string{
		"runtime.go.cpu.usage.percent", "runtime.go.threads.count",
		"runtime.go.files.", "runtime.go.memory.available", "runtime.go.memory.total",
	} {
		assert.False(t, rec.hasPrefix(p), p)
	}
	_, ok := rec.get("runtime.go.memory.usage.bytes", "shared")
	assert.False(t, ok)

	for _, o := range rec.list {
		assert.Equal(t, models.MetricTypeGauge, o.mtype, o.name)
	}
}

func TestDarwinProcessCPUPercent(t *testing.T) {
	rec := &recorder{}
	user := 3 * time.Second
	read := func(ru *syscall.Rusage) error { return fakeRusage(user, time.Second)(ru) }
	c := darwinCollector(read, rec, nil)
	now := time.Unix(1000, 0)
	c.proc.now = func() time.Time { return now }

	c.Collect()
	_, ok := rec.get("runtime.go.cpu.usage.percent", "")
	require.False(t, ok, "first sample must be skipped")

	// +1s CPU after 2s wall.
	user += time.Second
	now = now.Add(2 * time.Second)
	rec.reset()
	c.Collect()

	got, ok := rec.get("runtime.go.cpu.usage.percent", "")
	require.True(t, ok)
	assert.InDelta(t, 1.0/2.0/float64(runtime.GOMAXPROCS(0))*100, got, 1e-6)
}

func TestDarwinProcessRusageErrorReportedOnce(t *testing.T) {
	rec := &recorder{}
	el := &errLog{}
	boom := errors.New("boom")
	c := darwinCollector(func(*syscall.Rusage) error { return boom }, rec, el)

	c.Collect()
	c.Collect()
	c.Collect()

	require.Len(t, el.calls, 1, "%v", el.calls)
	assert.Contains(t, el.calls[0], "process: ")
	assert.Contains(t, el.calls[0], "boom")
	assert.False(t, rec.hasPrefix("runtime.go.cpu.usage"))
	assert.False(t, rec.hasPrefix("runtime.go.memory.usage"))
	assert.True(t, rec.hasPrefix("runtime.go.goroutines"), "regular metrics still emitted")
}

func TestDarwinProcessTotalAndSplitCPU(t *testing.T) {
	rec := &recorder{}
	user, system := 3*time.Second, time.Second
	read := func(ru *syscall.Rusage) error { return fakeRusage(user, system)(ru) }
	c := darwinCollector(read, rec, nil)
	now := time.Unix(1000, 0)
	c.proc.now = func() time.Time { return now }

	c.Collect()
	total, ok := rec.get("runtime.go.cpu.usage_total.seconds", "")
	require.True(t, ok)
	assert.InDelta(t, 4.0, total, 1e-6)
	_, ok = rec.get("runtime.go.cpu.usage_total.percent", "")
	assert.False(t, ok, "first sample must be skipped")

	user += 600 * time.Millisecond
	system += 400 * time.Millisecond
	now = now.Add(2 * time.Second)
	rec.reset()
	c.Collect()

	gomax := float64(runtime.GOMAXPROCS(0))
	for name, want := range map[string]float64{
		"cpu.usage_user.percent":   0.6 / 2 / gomax * 100,
		"cpu.usage_system.percent": 0.4 / 2 / gomax * 100,
		"cpu.usage_total.percent":  1.0 / 2 / gomax * 100,
	} {
		got, ok := rec.get("runtime.go."+name, "")
		require.True(t, ok, name)
		assert.InDelta(t, want, got, 1e-6, name)
	}
	assert.False(t, rec.hasPrefix("runtime.go.cpu.cgroup"), "cgroups are Linux only")
	assert.False(t, rec.hasPrefix("runtime.go.memory.virtual"), "rusage has no virtual size")
}
