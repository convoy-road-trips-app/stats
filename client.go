package stats

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats/runtimemetrics"
)

// clientCore holds the state shared by a root Client and every view derived
// from it: the pipeline, configuration, runtime collector and the shutdown
// coordination. All recording takes mu.RLock exactly once.
type clientCore struct {
	cfg       *Config
	pipeline  *Pipeline
	collector *runtimemetrics.Collector

	// Shutdown coordination
	shutdownOnce sync.Once
	closed       bool
	mu           sync.RWMutex
}

// Client is the main stats library client. It is a thin handle over a shared
// clientCore; NewClient returns the root handle.
type Client struct {
	core *clientCore
	root bool
}

// NewClient creates a new stats client with the given options
func NewClient(opts ...Option) (*Client, error) {
	// Defaults, then options, then OTEL_* environment for what options left open
	cfg, err := buildConfig(opts)
	if err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	// Validate configuration
	if err := ValidateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	// Create pipeline (which will create exporters based on config)
	pipeline, err := NewPipeline(cfg)
	if err != nil {
		return nil, fmt.Errorf("create pipeline: %w", err)
	}

	// Start pipeline
	if err := pipeline.Start(); err != nil {
		return nil, fmt.Errorf("start pipeline: %w", err)
	}

	client := &Client{
		core: &clientCore{
			cfg:      cfg,
			pipeline: pipeline,
		},
		root: true,
	}

	if cfg.RuntimeMetrics != nil && cfg.RuntimeMetrics.Enabled {
		record := func(name string, mtype MetricType, value float64) {
			ctx := context.Background()
			switch mtype {
			case MetricTypeGauge:
				_ = client.Gauge(ctx, name, value)
			case MetricTypeCounter:
				_ = client.Counter(ctx, name, value)
			case MetricTypeHistogram:
				_ = client.Histogram(ctx, name, value)
			}
		}
		client.core.collector = runtimemetrics.New(
			runtimemetrics.Config{
				CollectInterval: cfg.RuntimeMetrics.CollectInterval,
				Prefix:          cfg.RuntimeMetrics.Prefix,
			},
			record,
		)
		client.core.collector.Start()
	}

	return client, nil
}

// Counter records a counter metric
// Context is propagated for cancellation, deadlines, and tracing
func (c *Client) Counter(ctx context.Context, name string, value float64, opts ...MetricOption) error {
	return c.recordValue(ctx, MetricTypeCounter, name, value, opts)
}

// Gauge records a gauge metric
// Context is propagated for cancellation, deadlines, and tracing
func (c *Client) Gauge(ctx context.Context, name string, value float64, opts ...MetricOption) error {
	return c.recordValue(ctx, MetricTypeGauge, name, value, opts)
}

// Histogram records a histogram metric
// Context is propagated for cancellation, deadlines, and tracing
func (c *Client) Histogram(ctx context.Context, name string, value float64, opts ...MetricOption) error {
	return c.recordValue(ctx, MetricTypeHistogram, name, value, opts)
}

// RecordMetric records a pre-configured metric
func (c *Client) RecordMetric(ctx context.Context, m *Metric) error {
	return c.record(ctx, m, nil)
}

// recordValue validates the input, builds a pooled metric and records it. The
// metric returns to the pool when recording fails.
func (c *Client) recordValue(ctx context.Context, typ MetricType, name string, value float64, opts []MetricOption) error {
	if err := validateMetricInput(name, value); err != nil {
		// A closed client reports ErrClientClosed in preference to bad input.
		if c.core.isClosed() {
			return ErrClientClosed
		}
		return err
	}

	m := AcquireMetric()
	m.Name = name
	m.Type = typ
	m.Value = value
	m.Timestamp = time.Now()

	if err := c.record(ctx, m, opts); err != nil {
		ReleaseMetric(m)
		return err
	}
	return nil
}

// record is the single, non-recursive recording path. It takes core.mu.RLock
// once, fails with ErrClientClosed after shutdown, builds the attributes (context
// tags, then the metric's existing attributes, then the explicit options; the
// last value wins on a duplicate key) and hands m to the pipeline, which
// validates every key. It never releases m; the caller owns it on error.
func (c *Client) record(ctx context.Context, m *Metric, opts []MetricOption) error {
	core := c.core
	core.mu.RLock()
	defer core.mu.RUnlock()

	if core.closed {
		return ErrClientClosed
	}

	prependContextTags(ctx, m)
	for _, opt := range opts {
		opt(m)
	}

	return core.pipeline.Record(ctx, m)
}

// isClosed reports whether the core has begun shutting down.
func (core *clientCore) isClosed() bool {
	core.mu.RLock()
	defer core.mu.RUnlock()
	return core.closed
}

// Increment increments a counter by 1
// Context is propagated for cancellation, deadlines, and tracing
func (c *Client) Increment(ctx context.Context, name string, opts ...MetricOption) error {
	return c.Counter(ctx, name, 1.0, opts...)
}

// IncrementBy increments a counter by the given value
// Context is propagated for cancellation, deadlines, and tracing
func (c *Client) IncrementBy(ctx context.Context, name string, value float64, opts ...MetricOption) error {
	return c.Counter(ctx, name, value, opts...)
}

// Timing records a timing metric (histogram) in milliseconds
// Context is propagated for cancellation, deadlines, and tracing
func (c *Client) Timing(ctx context.Context, name string, duration time.Duration, opts ...MetricOption) error {
	ms := float64(duration.Milliseconds())
	return c.Histogram(ctx, name, ms, opts...)
}

// Stats returns client statistics
func (c *Client) Stats() ClientStats {
	core := c.core
	core.mu.RLock()
	defer core.mu.RUnlock()

	pipelineStats := core.pipeline.Stats()

	return ClientStats{
		ServiceName: core.cfg.ServiceName,
		Environment: core.cfg.Environment,
		Closed:      core.closed,
		Pipeline:    pipelineStats,
	}
}

// ClientStats contains statistics about the client
type ClientStats struct {
	ServiceName string
	Environment string
	Closed      bool
	Pipeline    PipelineStats
}

// Helper functions for creating metrics

// NewCounter creates a counter metric builder
func NewCounter(name string, value float64) *MetricBuilder {
	return &MetricBuilder{
		metric: &Metric{
			Name:       name,
			Type:       MetricTypeCounter,
			Value:      value,
			Attributes: make([]attribute.KeyValue, 0, 8),
			Timestamp:  time.Now(),
			Priority:   1,
		},
	}
}

// NewGauge creates a gauge metric builder
func NewGauge(name string, value float64) *MetricBuilder {
	return &MetricBuilder{
		metric: &Metric{
			Name:       name,
			Type:       MetricTypeGauge,
			Value:      value,
			Attributes: make([]attribute.KeyValue, 0, 8),
			Timestamp:  time.Now(),
			Priority:   1,
		},
	}
}

// NewHistogram creates a histogram metric builder
func NewHistogram(name string, value float64) *MetricBuilder {
	return &MetricBuilder{
		metric: &Metric{
			Name:       name,
			Type:       MetricTypeHistogram,
			Value:      value,
			Attributes: make([]attribute.KeyValue, 0, 8),
			Timestamp:  time.Now(),
			Priority:   1,
		},
	}
}

// MetricBuilder provides a fluent API for building metrics
type MetricBuilder struct {
	metric *Metric
}

// WithTag adds a tag/attribute to the metric
func (mb *MetricBuilder) WithTag(key, value string) *MetricBuilder {
	mb.metric.Attributes = append(mb.metric.Attributes, attribute.String(key, value))
	return mb
}

// WithTags adds multiple tags/attributes to the metric
func (mb *MetricBuilder) WithTags(tags map[string]string) *MetricBuilder {
	for k, v := range tags {
		mb.metric.Attributes = append(mb.metric.Attributes, attribute.String(k, v))
	}
	return mb
}

// WithPriority sets the priority of the metric
func (mb *MetricBuilder) WithPriority(priority int) *MetricBuilder {
	mb.metric.Priority = priority
	return mb
}

// Build returns the built metric
func (mb *MetricBuilder) Build() *Metric {
	return mb.metric
}

// validateMetricInput validates metric name and value
func validateMetricInput(name string, value float64) error {
	// Validate metric name
	if name == "" {
		return fmt.Errorf("%w: metric name cannot be empty", ErrInvalidConfig)
	}

	// Prevent excessively long names (DoS protection)
	if len(name) > 256 {
		return fmt.Errorf("%w: metric name exceeds maximum length (256 characters)", ErrInvalidConfig)
	}

	// Validate value is not NaN or Inf
	if math.IsNaN(value) {
		return fmt.Errorf("%w: metric value cannot be NaN", ErrInvalidConfig)
	}

	if math.IsInf(value, 0) {
		return fmt.Errorf("%w: metric value cannot be Inf", ErrInvalidConfig)
	}

	return nil
}
