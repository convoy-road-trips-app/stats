package stats

import (
	"context"
	"time"
)

// DurationObserver is implemented by recorders that record a duration as a
// histogram in seconds. It is separate from Recorder so that existing Recorder
// implementations keep compiling. *Client and *NoOpClient implement it; check
// for it with a type assertion:
//
//	if o, ok := recorder.(stats.DurationObserver); ok {
//		err = o.Observe(ctx, "op.duration", time.Since(start))
//	}
type DurationObserver interface {
	Observe(ctx context.Context, name string, d time.Duration, opts ...MetricOption) error
}

// Observe records d as a histogram metric in seconds (d.Seconds()), the unit
// OpenTelemetry semantic conventions and Prometheus use for durations. It takes
// the same recording path as Histogram, so it never blocks, applies opts and
// context tags the same way, and returns ErrClientClosed after Close or
// Shutdown. Prefer it to Timing, which records milliseconds.
func (c *Client) Observe(ctx context.Context, name string, d time.Duration, opts ...MetricOption) error {
	return c.Histogram(ctx, name, d.Seconds(), opts...)
}

// Observe does nothing and returns nil.
func (n *NoOpClient) Observe(ctx context.Context, name string, d time.Duration, opts ...MetricOption) error {
	return nil
}

// Compile-time checks that the built-in recorders implement DurationObserver.
var (
	_ DurationObserver = (*Client)(nil)
	_ DurationObserver = (*NoOpClient)(nil)
)
