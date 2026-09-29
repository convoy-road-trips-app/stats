package stats

import (
	"fmt"
	"time"

	"github.com/convoy-road-trips-app/stats/models"
)

// Re-export config types from models package for backwards compatibility
type (
	// Config is the main configuration struct
	Config = models.Config
	// CloudWatchConfig is the CloudWatch configuration struct
	CloudWatchConfig = models.CloudWatchConfig
	// PrometheusConfig is the Prometheus configuration struct
	PrometheusConfig = models.PrometheusConfig
	// DatadogConfig is the Datadog configuration struct
	DatadogConfig = models.DatadogConfig
	// OTLPConfig is the OTLP configuration struct
	OTLPConfig = models.OTLPConfig
	// RuntimeMetricsConfig is the runtime metrics configuration struct
	RuntimeMetricsConfig = models.RuntimeMetricsConfig
	// OTLPProtocol selects gRPC or HTTP transport
	OTLPProtocol = models.OTLPProtocol
	// Temporality selects cumulative or delta metric export.
	Temporality = models.Temporality
	// DropStrategy is the drop strategy enum
	DropStrategy = models.DropStrategy
)

const (
	// DropNewest is the drop strategy that drops the newest metrics
	DropNewest = models.DropNewest
	// DropOldest is the drop strategy that drops the oldest metrics
	DropOldest = models.DropOldest

	// OTLPProtocolGRPC selects gRPC transport (port 4317)
	OTLPProtocolGRPC = models.OTLPProtocolGRPC
	// OTLPProtocolHTTP selects HTTP/protobuf transport (port 4318)
	OTLPProtocolHTTP = models.OTLPProtocolHTTP
	// Cumulative exports cumulative metric values.
	Cumulative = models.Cumulative
	// Delta exports metric values since the previous collection.
	Delta = models.Delta
)

// DefaultConfig returns a configuration with sensible defaults
func DefaultConfig() *Config {
	return &Config{
		ServiceName:      "unknown-service",
		Environment:      "development",
		BufferSize:       16384, // 16K ring buffer
		Workers:          4,
		FlushInterval:    100 * time.Millisecond,
		UDPTimeout:       100 * time.Millisecond,
		MaxMemoryBytes:   10 * 1024 * 1024, // 10MB
		MaxCardinality:   defaultMaxCardinality,
		DropStrategy:     DropNewest,
		AdaptiveBatching: false,
	}
}

// ValidateConfig checks if the configuration is valid
func ValidateConfig(c *Config) error {
	if c.ServiceName == "" {
		return fmt.Errorf("%w: service name is required", ErrInvalidConfig)
	}

	if c.BufferSize <= 0 {
		return fmt.Errorf("%w: buffer size must be positive", ErrInvalidConfig)
	}

	if c.Workers <= 0 {
		return fmt.Errorf("%w: workers must be positive", ErrInvalidConfig)
	}

	if c.FlushInterval <= 0 {
		return fmt.Errorf("%w: flush interval must be positive", ErrInvalidConfig)
	}

	if c.MaxMemoryBytes <= 0 {
		return fmt.Errorf("%w: max memory bytes must be positive", ErrInvalidConfig)
	}

	if c.MaxCardinality < 0 {
		return fmt.Errorf("%w: max cardinality must not be negative", ErrInvalidConfig)
	}

	// Validate backend configs
	if c.CloudWatch != nil && c.CloudWatch.Enabled {
		if err := c.CloudWatch.Validate(); err != nil {
			return fmt.Errorf("cloudwatch config: %w", err)
		}
	}

	if c.Prometheus != nil && c.Prometheus.Enabled {
		if err := c.Prometheus.Validate(); err != nil {
			return fmt.Errorf("prometheus config: %w", err)
		}
	}

	if c.Datadog != nil && c.Datadog.Enabled {
		if err := c.Datadog.Validate(); err != nil {
			return fmt.Errorf("datadog config: %w", err)
		}
	}

	if c.OTLP != nil && (c.OTLP.Enabled || c.OTLP.HistogramBuckets != nil || c.OTLP.Temporality != "" || len(c.OTLP.ResourceAttributes) > 0) {
		if err := c.OTLP.Validate(); err != nil {
			return fmt.Errorf("otlp config: %w", err)
		}
	}

	if c.RuntimeMetrics != nil && c.RuntimeMetrics.Enabled {
		if err := c.RuntimeMetrics.Validate(); err != nil {
			return fmt.Errorf("runtime metrics config: %w", err)
		}
	}

	return nil
}
