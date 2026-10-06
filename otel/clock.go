package otel

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/convoy-road-trips-app/stats"
)

// Clock reports the durations of sequential execution steps to a
// Float64Histogram, in seconds, with a "stamp" attribute naming the step.
// It works with any OpenTelemetry histogram, not only this SDK's. Ported from
// segmentio/stats.
//
// A Clock measures one sequence of steps and is not safe for concurrent use.
// Use constant step names: every distinct name is a new series.
type Clock struct {
	histogram metric.Float64Histogram
	first     time.Time
	last      time.Time
	opts      []metric.RecordOption
}

// NewClock returns a clock started now. opts, for example
// metric.WithAttributes, apply to every observation.
func NewClock(histogram metric.Float64Histogram, opts ...metric.RecordOption) *Clock {
	return NewClockAt(histogram, time.Now(), opts...)
}

// NewClockAt returns a clock started at start.
func NewClockAt(histogram metric.Float64Histogram, start time.Time, opts ...metric.RecordOption) *Clock {
	cpy := make([]metric.RecordOption, len(opts), len(opts)+1) // observe always appends the stamp
	copy(cpy, opts)
	return &Clock{histogram: histogram, first: start, last: start, opts: cpy}
}

// Stamp records the time since the previous Stamp, or since the clock started,
// with the stamp attribute set to name.
func (c *Clock) Stamp(ctx context.Context, name string) {
	c.StampAt(ctx, name, time.Now())
}

// StampAt is Stamp with an explicit current time.
func (c *Clock) StampAt(ctx context.Context, name string, now time.Time) {
	d := now.Sub(c.last)
	c.last = now
	c.observe(ctx, name, d)
}

// Stop records the time since the clock started with the stamp attribute set
// to "total".
func (c *Clock) Stop(ctx context.Context) {
	c.StopAt(ctx, time.Now())
}

// StopAt is Stop with an explicit current time.
func (c *Clock) StopAt(ctx context.Context, now time.Time) {
	c.observe(ctx, stats.StampTotal, now.Sub(c.first))
}

func (c *Clock) observe(ctx context.Context, stamp string, d time.Duration) {
	c.histogram.Record(ctx, d.Seconds(), append(c.opts, metric.WithAttributes(attribute.String(stats.StampTag, stamp)))...)
}
