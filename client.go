package stats

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
)

// Client is the main stats library client
type Client struct {
	cfg      *Config
	pipeline *Pipeline

	// Shutdown coordination
	shutdownOnce sync.Once
	closed       bool
	mu           sync.RWMutex
}

// NewClient creates a new stats client with the given options
func NewClient(opts ...Option) (*Client, error) {
	// Start with default config
	cfg := DefaultConfig()

	// Apply options
	for _, opt := range opts {
		opt(cfg)
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
		cfg:      cfg,
		pipeline: pipeline,
	}

	return client, nil
}

// Counter records a counter metric
// Context is propagated for cancellation, deadlines, and tracing
func (c *Client) Counter(ctx context.Context, name string, value float64, opts ...MetricOption) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.closed {
		return ErrClientClosed
	}

	// Validate input
	if err := validateMetricInput(name, value); err != nil {
		return err
	}

	// Acquire metric from pool
	m := AcquireMetric()
	m.Name = name
	m.Type = MetricTypeCounter
	m.Value = value
	m.Timestamp = time.Now()

	// Apply options
	for _, opt := range opts {
		opt(m)
	}

	// Record metric with context
	if err := c.pipeline.Record(ctx, m); err != nil {
		// Return metric to pool if recording failed
		ReleaseMetric(m)
		return err
	}

	return nil
}

// Gauge records a gauge metric
// Context is propagated for cancellation, deadlines, and tracing
func (c *Client) Gauge(ctx context.Context, name string, value float64, opts ...MetricOption) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.closed {
		return ErrClientClosed
	}

	// Validate input
	if err := validateMetricInput(name, value); err != nil {
		return err
	}

	m := AcquireMetric()
	m.Name = name
	m.Type = MetricTypeGauge
	m.Value = value
	m.Timestamp = time.Now()

	for _, opt := range opts {
		opt(m)
	}

	if err := c.pipeline.Record(ctx, m); err != nil {
		ReleaseMetric(m)
		return err
	}

	return nil
}

// Histogram records a histogram metric
// Context is propagated for cancellation, deadlines, and tracing
func (c *Client) Histogram(ctx context.Context, name string, value float64, opts ...MetricOption) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.closed {
		return ErrClientClosed
	}

	// Validate input
	if err := validateMetricInput(name, value); err != nil {
		return err
	}

	m := AcquireMetric()
	m.Name = name
	m.Type = MetricTypeHistogram
	m.Value = value
	m.Timestamp = time.Now()

	for _, opt := range opts {
		opt(m)
	}

	if err := c.pipeline.Record(ctx, m); err != nil {
		ReleaseMetric(m)
		return err
	}

	return nil
}

// RecordMetric records a pre-configured metric
func (c *Client) RecordMetric(ctx context.Context, m *Metric) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.closed {
		return ErrClientClosed
	}

	return c.pipeline.Record(ctx, m)
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
	c.mu.RLock()
	defer c.mu.RUnlock()

	pipelineStats := c.pipeline.Stats()

	return ClientStats{
		ServiceName: c.cfg.ServiceName,
		Environment: c.cfg.Environment,
		Closed:      c.closed,
		Pipeline:    pipelineStats,
	}
}

// Shutdown gracefully shuts down the client
func (c *Client) Shutdown(ctx context.Context) error {
	var shutdownErr error

	c.shutdownOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()

		// Shutdown pipeline
		shutdownErr = c.pipeline.Shutdown(ctx)
	})

	return shutdownErr
}

// Close closes the client with a default 5-second timeout
func (c *Client) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.Shutdown(ctx)
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
