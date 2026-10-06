package runtimemetrics

import (
	"os"
	"time"

	"github.com/convoy-road-trips-app/stats/models"
)

// delayReader reads the cumulative delay totals of a process. It is Get in
// production and a fake in tests.
type delayReader func(pid int) (DelayInfo, error)

// delayState holds the state delay metrics keep between collections.
type delayState struct {
	read delayReader
	pid  int

	// prev holds the previous cumulative totals. It is zero until the first
	// successful read, so the first collection emits the full totals.
	prev DelayInfo
	// disabled is set after the first read failure and never cleared.
	disabled bool
}

func newDelayState() *delayState {
	return &delayState{read: Get, pid: os.Getpid()}
}

// delayIncrement returns the counter increment for a cumulative kernel total.
// A decrease means the total was reset, so the new total is the increment.
// The result is never negative.
func delayIncrement(cur, prev time.Duration) time.Duration {
	if cur < prev {
		return max(cur, 0)
	}
	return cur - prev
}

// collectDelay emits the kernel delay totals as counter increments. On the
// first read error it calls OnError("delay", err) once and disables delay
// collection for good, including on unsupported platforms and on EPERM. The
// caller holds c.mu.
func (c *Collector) collectDelay() {
	d := c.delay
	if !c.cfg.DelayMetrics || d == nil || d.disabled {
		return
	}
	cur, err := d.read(d.pid)
	if err != nil {
		d.disabled = true
		if c.cfg.OnError != nil {
			c.cfg.OnError("delay", err)
		}
		return
	}

	prefix := c.prefix()
	for _, m := range []struct {
		name     string
		cur, old time.Duration
	}{
		{"cpu.delay.seconds", cur.CPU, d.prev.CPU},
		{"blockio.delay.seconds", cur.BlockIO, d.prev.BlockIO},
		{"swapin.delay.seconds", cur.SwapIn, d.prev.SwapIn},
		{"freepages.delay.seconds", cur.FreePages, d.prev.FreePages},
	} {
		c.record(prefix+m.name, models.MetricTypeCounter, delayIncrement(m.cur, m.old).Seconds())
	}
	d.prev = cur
}
