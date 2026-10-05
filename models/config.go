package models

import (
	"fmt"
	"time"
)

// DropStrategy defines how to handle new metrics when the buffer is full
type DropStrategy int

const (
	// DropNewest drops the incoming metric (default)
	DropNewest DropStrategy = iota
	// DropOldest drops the oldest metric in the buffer to make room
	DropOldest
)

// Config holds the complete configuration for the stats client
type Config struct {
	// Global configuration
	ServiceName   string
	Environment   string
	BufferSize    int
	Workers       int
	FlushInterval time.Duration
	// FlushIntervalSet reports that the flush interval was chosen with an option,
	// so OTEL_METRIC_EXPORT_INTERVAL must not replace it.
	FlushIntervalSet bool
	UDPTimeout       time.Duration
	MaxMemoryBytes   int64
	MaxCardinality   int

	// Backpressure configuration
	DropStrategy     DropStrategy
	AdaptiveBatching bool

	// HistogramBucketsByName holds explicit histogram bounds per metric name,
	// in the units the metric is recorded in. See BucketsFor for precedence.
	HistogramBucketsByName map[string][]float64

	// Rate limiting (0 = disabled)
	RateLimitPerSecond float64 // Metrics per second (0 = unlimited)
	RateLimitBurst     int     // Maximum burst size

	// Backend configurations
	CloudWatch *CloudWatchConfig
	Prometheus *PrometheusConfig
	Datadog    *DatadogConfig
	OTLP       *OTLPConfig
	// OTLPOverrides holds the OTLP settings stated through options, in option
	// order. NewClient resolves each field as: override, else OTEL_* environment, else default.
	OTLPOverrides  OTLPOverrides
	RuntimeMetrics *RuntimeMetricsConfig

	// Exporters are custom exporters registered after the built-in ones.
	Exporters []Exporter
}

// CloudWatchConfig configures the CloudWatch exporter
type CloudWatchConfig struct {
	Enabled   bool
	AgentHost string
	AgentPort int
	Namespace string
	Region    string
}

// PrometheusConfig configures the Prometheus exporter
type PrometheusConfig struct {
	Enabled            bool
	PushgatewayAddress string
	Job                string
	Instance           string
}

// DatadogConfig configures the Datadog exporter
type DatadogConfig struct {
	Enabled   bool
	AgentHost string
	AgentPort int
	Tags      []string
}

// RuntimeMetricsConfig configures runtime metrics collection
type RuntimeMetricsConfig struct {
	Enabled         bool
	CollectInterval time.Duration
	Prefix          string
	ProcessMetrics  bool
	DelayMetrics    bool
}

// DefaultRuntimeMetricsConfig returns the default runtime metrics configuration
func DefaultRuntimeMetricsConfig() *RuntimeMetricsConfig {
	return &RuntimeMetricsConfig{
		Enabled:         false,
		CollectInterval: 10 * time.Second,
		Prefix:          "runtime.go",
	}
}

// Address returns the full UDP address for CloudWatch
func (c *CloudWatchConfig) Address() string {
	return fmt.Sprintf("%s:%d", c.AgentHost, c.AgentPort)
}

// Address returns the full UDP address for Datadog
func (c *DatadogConfig) Address() string {
	return fmt.Sprintf("%s:%d", c.AgentHost, c.AgentPort)
}

// Validate checks if the CloudWatch configuration is valid
func (c *CloudWatchConfig) Validate() error {
	if c.AgentHost == "" {
		return fmt.Errorf("cloudwatch: agent host is required")
	}
	if c.AgentPort <= 0 || c.AgentPort > 65535 {
		return fmt.Errorf("cloudwatch: invalid agent port")
	}
	if c.Namespace == "" {
		return fmt.Errorf("cloudwatch: namespace is required")
	}
	return nil
}

// Validate checks if the Prometheus configuration is valid
func (c *PrometheusConfig) Validate() error {
	if c.PushgatewayAddress == "" {
		return fmt.Errorf("prometheus: pushgateway address is required")
	}
	return nil
}

// Validate checks if the Datadog configuration is valid
func (c *DatadogConfig) Validate() error {
	if c.AgentHost == "" {
		return fmt.Errorf("datadog: agent host is required")
	}
	if c.AgentPort <= 0 || c.AgentPort > 65535 {
		return fmt.Errorf("datadog: invalid agent port")
	}
	return nil
}

// ApplyDefaults fills in zero-value runtime metrics configuration fields
func (c *RuntimeMetricsConfig) ApplyDefaults() {
	if c.CollectInterval <= 0 {
		c.CollectInterval = 10 * time.Second
	}
	if c.Prefix == "" {
		c.Prefix = "runtime.go"
	}
}

// Validate checks if the runtime metrics configuration is valid
func (c *RuntimeMetricsConfig) Validate() error {
	if c.CollectInterval <= 0 {
		return fmt.Errorf("runtime metrics: collect interval must be greater than 0")
	}
	if c.Prefix == "" {
		return fmt.Errorf("runtime metrics: prefix is required")
	}
	return nil
}
