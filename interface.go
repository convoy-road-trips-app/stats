package stats

import (
	"context"
	"time"
)

// Recorder is the interface for stats recording.
// It is implemented by *Client and *NoOpClient.
type Recorder interface {
	Counter(ctx context.Context, name string, value float64, opts ...MetricOption) error
	Gauge(ctx context.Context, name string, value float64, opts ...MetricOption) error
	Histogram(ctx context.Context, name string, value float64, opts ...MetricOption) error
	RecordMetric(ctx context.Context, m *Metric) error
	Increment(ctx context.Context, name string, opts ...MetricOption) error
	IncrementBy(ctx context.Context, name string, value float64, opts ...MetricOption) error
	Timing(ctx context.Context, name string, duration time.Duration, opts ...MetricOption) error
	Stats() ClientStats
	Shutdown(ctx context.Context) error
	Close() error
}

// Flusher is implemented by recorders that can export what they have buffered
// before the process or invocation ends. It is separate from Recorder so that
// existing Recorder implementations keep compiling. *Client and *NoOpClient
// implement it; check for it with a type assertion:
//
//	if f, ok := recorder.(stats.Flusher); ok {
//		err = f.Flush(ctx)
//	}
type Flusher interface {
	Flush(ctx context.Context) error
}

// Ensure implementations satisfy the interface
var (
	_ Recorder = (*Client)(nil)
	_ Recorder = (*NoOpClient)(nil)
	_ Flusher  = (*Client)(nil)
	_ Flusher  = (*NoOpClient)(nil)
)
