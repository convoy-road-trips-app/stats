package runtimemetrics

import (
	"errors"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/convoy-road-trips-app/stats/models"
)

// fakeDelay returns each queued DelayInfo in turn, then repeats the last.
type fakeDelay struct {
	infos []DelayInfo
	err   error
	calls int
	pids  []int
}

func (f *fakeDelay) read(pid int) (DelayInfo, error) {
	f.calls++
	f.pids = append(f.pids, pid)
	if f.err != nil {
		return DelayInfo{}, f.err
	}
	i := min(f.calls-1, len(f.infos)-1)
	return f.infos[i], nil
}

func newDelayCollector(f *fakeDelay, rec *recorder, el *errLog) *Collector {
	cfg := Config{Prefix: "runtime.go", DelayMetrics: true}
	if el != nil {
		cfg.OnError = el.onError
	}
	c := New(cfg, rec.record)
	c.delay.read = f.read
	return c
}

func TestDelayCountersAreIncrements(t *testing.T) {
	f := &fakeDelay{infos: []DelayInfo{
		{CPU: 2 * time.Second, BlockIO: time.Second, SwapIn: 500 * time.Millisecond, FreePages: 250 * time.Millisecond},
		{CPU: 5 * time.Second, BlockIO: time.Second, SwapIn: 500 * time.Millisecond, FreePages: 750 * time.Millisecond},
	}}
	rec := &recorder{}
	c := newDelayCollector(f, rec, nil)

	c.Collect()
	for name, want := range map[string]float64{
		"runtime.go.cpu.delay.seconds":       2,
		"runtime.go.blockio.delay.seconds":   1,
		"runtime.go.swapin.delay.seconds":    0.5,
		"runtime.go.freepages.delay.seconds": 0.25,
	} {
		got, ok := rec.get(name, "")
		require.True(t, ok, name)
		assert.InDelta(t, want, got, 1e-9, "first collect emits the total: %s", name)
	}
	for name := range delayNames {
		for _, o := range rec.list {
			if o.name == name {
				assert.Equal(t, models.MetricTypeCounter, o.mtype, name)
			}
		}
	}

	rec.reset()
	c.Collect()
	for name, want := range map[string]float64{
		"runtime.go.cpu.delay.seconds":       3,
		"runtime.go.blockio.delay.seconds":   0,
		"runtime.go.swapin.delay.seconds":    0,
		"runtime.go.freepages.delay.seconds": 0.5,
	} {
		got, ok := rec.get(name, "")
		require.True(t, ok, name)
		assert.InDelta(t, want, got, 1e-9, "second collect emits the increment: %s", name)
	}
	assert.Equal(t, 2, f.calls)
}

var delayNames = map[string]struct{}{
	"runtime.go.cpu.delay.seconds":       {},
	"runtime.go.blockio.delay.seconds":   {},
	"runtime.go.swapin.delay.seconds":    {},
	"runtime.go.freepages.delay.seconds": {},
}

func TestDelayResetRecordsCurrent(t *testing.T) {
	f := &fakeDelay{infos: []DelayInfo{
		{CPU: 10 * time.Second},
		{CPU: 2 * time.Second},
		{CPU: 3 * time.Second},
	}}
	rec := &recorder{}
	c := newDelayCollector(f, rec, nil)

	c.Collect()
	rec.reset()
	c.Collect()
	got, ok := rec.get("runtime.go.cpu.delay.seconds", "")
	require.True(t, ok)
	assert.InDelta(t, 2.0, got, 1e-9, "a decrease is a reset and records the current total")

	rec.reset()
	c.Collect()
	got, _ = rec.get("runtime.go.cpu.delay.seconds", "")
	assert.InDelta(t, 1.0, got, 1e-9, "increments resume from the reset total")

	for _, o := range rec.list {
		assert.GreaterOrEqual(t, o.value, 0.0, o.name)
	}
}

func TestDelayIncrementNeverNegative(t *testing.T) {
	assert.Equal(t, time.Duration(0), delayIncrement(0, 0))
	assert.Equal(t, 2*time.Second, delayIncrement(5*time.Second, 3*time.Second))
	assert.Equal(t, time.Second, delayIncrement(time.Second, 4*time.Second))
	assert.Equal(t, time.Duration(0), delayIncrement(-time.Second, time.Second))
}

func TestDelayDisabledOnceOnError(t *testing.T) {
	f := &fakeDelay{err: syscall.EPERM}
	rec := &recorder{}
	el := &errLog{}
	c := newDelayCollector(f, rec, el)

	for range 5 {
		c.Collect()
	}

	assert.Equal(t, 1, f.calls, "exactly one Get attempt")
	require.Len(t, el.calls, 1, "exactly one OnError call: %v", el.calls)
	assert.Contains(t, el.calls[0], "delay: ")
	assert.False(t, rec.hasPrefix("runtime.go.cpu.delay"))
	assert.False(t, rec.hasPrefix("runtime.go.blockio.delay"))
	assert.False(t, rec.hasPrefix("runtime.go.swapin.delay"))
	assert.False(t, rec.hasPrefix("runtime.go.freepages.delay"))
	assert.True(t, rec.hasPrefix("runtime.go.goroutines"), "regular metrics still emitted")
}

func TestDelayDisabledAfterLaterError(t *testing.T) {
	boom := errors.New("boom")
	calls := 0
	rec := &recorder{}
	el := &errLog{}
	c := New(Config{Prefix: "runtime.go", DelayMetrics: true, OnError: el.onError}, rec.record)
	c.delay.read = func(int) (DelayInfo, error) {
		calls++
		if calls == 2 {
			return DelayInfo{}, boom
		}
		return DelayInfo{CPU: time.Second}, nil
	}

	c.Collect()
	c.Collect()
	rec.reset()
	c.Collect()
	c.Collect()

	assert.Equal(t, 2, calls, "no retry after a failure")
	assert.Len(t, el.calls, 1)
	assert.False(t, rec.hasPrefix("runtime.go.cpu.delay"))
}

func TestDelayMetricsOffNeverReads(t *testing.T) {
	f := &fakeDelay{infos: []DelayInfo{{CPU: time.Second}}}
	rec := &recorder{}
	c := New(Config{Prefix: "runtime.go"}, rec.record)
	require.Nil(t, c.delay)
	c.delay = &delayState{read: f.read}
	c.Collect()
	c.Collect()

	assert.Zero(t, f.calls)
	assert.False(t, rec.hasPrefix("runtime.go.cpu.delay"))
}

func TestDelayReadsOwnPID(t *testing.T) {
	f := &fakeDelay{infos: []DelayInfo{{}}}
	c := newDelayCollector(f, &recorder{}, nil)
	c.Collect()
	require.Len(t, f.pids, 1)
	assert.Positive(t, f.pids[0])
}

func TestDelayNoOnErrorCallbackIsSafe(t *testing.T) {
	f := &fakeDelay{err: errTaskstatsUnsupported}
	c := newDelayCollector(f, &recorder{}, nil)
	c.Collect()
	c.Collect()
	assert.Equal(t, 1, f.calls)
}
