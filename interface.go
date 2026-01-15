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

// Ensure implementations satisfy the interface
var _ Recorder = (*Client)(nil)
var _ Recorder = (*NoOpClient)(nil)
