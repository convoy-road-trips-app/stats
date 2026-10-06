package stats

import (
	"context"
	"time"
)

// StampTag is the attribute key that identifies the step a Clock observation measures.
const StampTag = "stamp"

// StampTotal is the stamp value of the observation recorded by Clock.Stop.
const StampTotal = "total"

// Clock reports the durations of sequential execution steps as one histogram,
// in seconds, with a "stamp" attribute naming the step. Ported from
// segmentio/stats.
//
// A Clock measures one sequence of steps and is not safe for concurrent use.
// Use constant step names: every distinct name is a new series.
type Clock struct {
	recorder Recorder
	name     string
	first    time.Time
	last     time.Time
	opts     []MetricOption
}

// NewClock returns a clock started now that records to name through recorder.
func NewClock(recorder Recorder, name string, opts ...MetricOption) *Clock {
	return NewClockAt(recorder, name, time.Now(), opts...)
}

// NewClockAt returns a clock started at start.
func NewClockAt(recorder Recorder, name string, start time.Time, opts ...MetricOption) *Clock {
	cpy := make([]MetricOption, len(opts), len(opts)+1) // observe always appends the stamp
	copy(cpy, opts)
	return &Clock{recorder: recorder, name: name, first: start, last: start, opts: cpy}
}

// Clock returns a clock started now that records to name through c.
func (c *Client) Clock(name string, opts ...MetricOption) *Clock {
	return NewClock(c, name, opts...)
}

// Stamp records the time since the previous Stamp, or since the clock started,
// with the stamp attribute set to name.
func (c *Clock) Stamp(ctx context.Context, name string) error {
	return c.StampAt(ctx, name, time.Now())
}

// StampAt is Stamp with an explicit current time.
func (c *Clock) StampAt(ctx context.Context, name string, now time.Time) error {
	d := now.Sub(c.last)
	c.last = now
	return c.observe(ctx, name, d)
}

// Stop records the time since the clock started with the stamp attribute set
// to "total".
func (c *Clock) Stop(ctx context.Context) error {
	return c.StopAt(ctx, time.Now())
}

// StopAt is Stop with an explicit current time.
func (c *Clock) StopAt(ctx context.Context, now time.Time) error {
	return c.observe(ctx, StampTotal, now.Sub(c.first))
}

func (c *Clock) observe(ctx context.Context, stamp string, d time.Duration) error {
	opts := make([]MetricOption, len(c.opts), len(c.opts)+1)
	copy(opts, c.opts)
	opts = append(opts, WithAttribute(StampTag, stamp))
	if o, ok := c.recorder.(DurationObserver); ok {
		return o.Observe(ctx, c.name, d, opts...)
	}
	return c.recorder.Histogram(ctx, c.name, d.Seconds(), opts...)
}
