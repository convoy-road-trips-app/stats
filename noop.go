package stats

import (
	"context"
	"time"
)

// NoOpClient is a no-op implementation of Recorder.
type NoOpClient struct{}

// NewNoOpClient creates a new no-op stats client.
func NewNoOpClient() *NoOpClient {
	return &NoOpClient{}
}

func (n *NoOpClient) Counter(ctx context.Context, name string, value float64, opts ...MetricOption) error {
	return nil
}

func (n *NoOpClient) Gauge(ctx context.Context, name string, value float64, opts ...MetricOption) error {
	return nil
}

func (n *NoOpClient) Histogram(ctx context.Context, name string, value float64, opts ...MetricOption) error {
	return nil
}

func (n *NoOpClient) RecordMetric(ctx context.Context, m *Metric) error {
	return nil
}

func (n *NoOpClient) Increment(ctx context.Context, name string, opts ...MetricOption) error {
	return nil
}

func (n *NoOpClient) IncrementBy(ctx context.Context, name string, value float64, opts ...MetricOption) error {
	return nil
}

func (n *NoOpClient) Timing(ctx context.Context, name string, duration time.Duration, opts ...MetricOption) error {
	return nil
}

func (n *NoOpClient) Stats() ClientStats {
	return ClientStats{}
}

func (n *NoOpClient) Shutdown(ctx context.Context) error {
	return nil
}

func (n *NoOpClient) Close() error {
	return nil
}
