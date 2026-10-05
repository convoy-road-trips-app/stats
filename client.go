package stats

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
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

	// disabled is set once by NewClient when OTEL_SDK_DISABLED is true. A
	// disabled core has no pipeline, no exporters and no collector, and every
	// operation on it is a no-op; it is immutable, so it is read without mu.
	disabled bool

	// Shutdown coordination
	shutdownOnce sync.Once
	closed       bool
	mu           sync.RWMutex
}

// Client is the main stats library client. It is a thin handle over a shared
// clientCore; NewClient returns the root handle, and WithPrefix and WithTags
// return immutable views that record through the same core.
type Client struct {
	core   *clientCore
	prefix string               // prepended, joined with ".", to every metric name
	tags   []attribute.KeyValue // applied before context tags; never mutated
	root   bool                 // true only for the client NewClient returned
}

// NewClient creates a new stats client with the given options
//
// When the OTEL_SDK_DISABLED environment variable is "true" (case-insensitive,
// surrounding space ignored, as the OpenTelemetry specification defines it)
// NewClient returns a disabled client without reading the options or the other
// OTEL_* variables: no pipeline or exporter is created, nothing is dialed, and
// every method does nothing and returns nil. See Disabled.
func NewClient(opts ...Option) (*Client, error) {
	if sdkDisabled() {
		return &Client{core: &clientCore{cfg: DefaultConfig(), disabled: true}, root: true}, nil
	}

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
		client.core.collector = runtimemetrics.New(client.runtimeConfig(cfg.RuntimeMetrics), client.runtimeRecord)
		client.core.collector.Start()
	}

	return client, nil
}

// Disabled reports whether the client was created while OTEL_SDK_DISABLED was
// true. A disabled client, and every view of it, records nothing and returns
// nil from every method.
func (c *Client) Disabled() bool {
	return c.core.disabled
}

// runtimeConfig builds the collector configuration. Collector failures are
// counted under ExporterErrors["runtimemetrics.<source>"].
func (c *Client) runtimeConfig(rc *RuntimeMetricsConfig) runtimemetrics.Config {
	return runtimemetrics.Config{
		CollectInterval: rc.CollectInterval,
		Prefix:          rc.Prefix,
		ProcessMetrics:  rc.ProcessMetrics,
		DelayMetrics:    rc.DelayMetrics,
		OnError: func(source string, _ error) {
			c.core.pipeline.RecordExporterError("runtimemetrics." + source)
		},
	}
}

// runtimeRecord is the runtimemetrics.RecordFunc that routes collector output
// into the pipeline.
func (c *Client) runtimeRecord(name string, mtype MetricType, value float64, attrs ...attribute.KeyValue) {
	ctx := context.Background()
	var opts []MetricOption
	if len(attrs) > 0 {
		opts = []MetricOption{withKeyValues(attrs)}
	}
	switch mtype {
	case MetricTypeGauge:
		_ = c.Gauge(ctx, name, value, opts...)
	case MetricTypeCounter:
		_ = c.Counter(ctx, name, value, opts...)
	case MetricTypeHistogram:
		_ = c.Histogram(ctx, name, value, opts...)
	}
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

// WithPrefix returns a view of c that prepends prefix to every metric name,
// joined to the parent's prefix and the metric name with ".". Empty parts are
// skipped, so WithPrefix("api").WithPrefix("v1") records "req" as "api.v1.req".
// opts add tags to the view, exactly as WithTags does; only their attributes
// are used.
//
// The view shares the parent's pipeline and lifecycle: Close and Shutdown on a
// view do nothing and return nil, Flush and Stats act on the root, and
// recording fails with ErrClientClosed once the root is closed. The view is
// immutable and safe for concurrent use.
func (c *Client) WithPrefix(prefix string, opts ...MetricOption) *Client {
	v := c.view(opts)
	v.prefix = joinName(c.prefix, prefix)
	return v
}

// WithTags returns a view of c that keeps its prefix and adds tags from opts
// (for example WithAttribute); other option effects are ignored. View tags are
// applied before context tags, the metric's own attributes and explicit
// options, and a later tag wins over an earlier one with the same key, so a
// child's tag overrides its parent's. Lifecycle is as for WithPrefix.
func (c *Client) WithTags(opts ...MetricOption) *Client {
	return c.view(opts)
}

// view returns a non-root copy of c whose tags are c's tags followed by the
// attributes opts add.
func (c *Client) view(opts []MetricOption) *Client {
	var scratch Metric
	for _, opt := range opts {
		opt(&scratch)
	}
	return &Client{
		core:   c.core,
		prefix: c.prefix,
		tags:   slices.Concat(c.tags, scratch.Attributes),
	}
}

// joinName joins the non-empty parts with ".".
func joinName(parts ...string) string {
	parts = slices.DeleteFunc(slices.Clone(parts), func(s string) bool { return s == "" })
	return strings.Join(parts, ".")
}

// recordValue validates the input, builds a pooled metric and records it. The
// metric returns to the pool when recording fails.
func (c *Client) recordValue(ctx context.Context, typ MetricType, name string, value float64, opts []MetricOption) error {
	if c.core.disabled {
		return nil
	}
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
// once, fails with ErrClientClosed after shutdown, prefixes and validates the
// name, builds the attributes (view tags, then context tags, then the metric's
// existing attributes, then the explicit options; the last value wins on a
// duplicate key) and hands m to the pipeline, which validates every key. It
// never releases m; the caller owns it on error, and m's name is then restored.
func (c *Client) record(ctx context.Context, m *Metric, opts []MetricOption) error {
	core := c.core
	if core.disabled {
		return nil
	}
	core.mu.RLock()
	defer core.mu.RUnlock()

	if core.closed {
		return ErrClientClosed
	}

	name := m.Name
	m.Name = joinName(c.prefix, name)
	if err := validateMetricName(m.Name); err != nil {
		m.Name = name
		return err
	}

	prependContextTags(ctx, m)
	m.Attributes = slices.Insert(m.Attributes, 0, c.tags...)
	for _, opt := range opts {
		opt(m)
	}

	if err := core.pipeline.Record(ctx, m); err != nil {
		m.Name = name
		return err
	}
	return nil
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

// Timing records a timing metric (histogram) in milliseconds, truncated to a
// whole millisecond. New code should use Observe, which records seconds without
// truncation and is the unit OpenTelemetry and Prometheus expect. Timing is kept
// unchanged for existing callers.
// Context is propagated for cancellation, deadlines, and tracing
func (c *Client) Timing(ctx context.Context, name string, duration time.Duration, opts ...MetricOption) error {
	ms := float64(duration.Milliseconds())
	return c.Histogram(ctx, name, ms, opts...)
}

// Stats returns client statistics. A disabled client, or a view of one, returns
// a zero ClientStats whose Pipeline.ExporterErrors is an empty, non-nil map.
func (c *Client) Stats() ClientStats {
	core := c.core
	if core.disabled {
		return ClientStats{Pipeline: PipelineStats{ExporterErrors: map[string]uint64{}}}
	}
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
	if err := validateMetricName(name); err != nil {
		return err
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

// validateMetricName rejects an empty name and one longer than 256 characters
// (DoS protection). record applies it to the full, prefixed name.
func validateMetricName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: metric name cannot be empty", ErrInvalidConfig)
	}
	if len(name) > 256 {
		return fmt.Errorf("%w: metric name exceeds maximum length (256 characters)", ErrInvalidConfig)
	}
	return nil
}
