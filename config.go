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
	// OTLPOverrides records the OTLP settings stated explicitly through options
	OTLPOverrides = models.OTLPOverrides
	// OTLPRetry bounds retries of failed OTLP exports
	OTLPRetry = models.OTLPRetry
	// OTLPExponentialHistogram configures base-2 exponential OTLP histograms
	OTLPExponentialHistogram = models.OTLPExponentialHistogram
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
			return fmt.Errorf("%w: datadog config: %w", ErrInvalidConfig, err)
		}
	}

	if err := validateOTLPConfig(c.OTLP); err != nil {
		return err
	}

	for name, bounds := range c.HistogramBucketsByName {
		if err := models.ValidateHistogramBuckets(bounds); err != nil {
			return fmt.Errorf("%w: histogram buckets for %q: %w", ErrInvalidConfig, name, err)
		}
	}

	if c.RuntimeMetrics != nil && c.RuntimeMetrics.Enabled {
		if err := c.RuntimeMetrics.Validate(); err != nil {
			return fmt.Errorf("runtime metrics config: %w", err)
		}
	}

	return validateCustomExporters(c)
}

// validateOTLPConfig validates the OTLP settings, if any, when OTLP is enabled
// or an option set one of them. Invalid exponential histogram settings return
// ErrInvalidConfig, even when OTLP is not enabled.
func validateOTLPConfig(o *OTLPConfig) error {
	if o == nil {
		return nil
	}
	if o.ExponentialHistogram != nil {
		if err := o.ExponentialHistogram.Validate(); err != nil {
			return fmt.Errorf("%w: otlp config: %w", ErrInvalidConfig, err)
		}
	}
	if o.Enabled || o.HistogramBuckets != nil || len(o.BucketsByName) > 0 || o.Temporality != "" || len(o.ResourceAttributes) > 0 || o.Retry != nil {
		if err := o.Validate(); err != nil {
			return fmt.Errorf("otlp config: %w", err)
		}
	}
	return nil
}

// validateCustomExporters rejects nil custom exporters and names that are used
// twice, including names of enabled built-in exporters: per-exporter error
// counts are keyed by name.
func validateCustomExporters(c *Config) error {
	if len(c.Exporters) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(c.Exporters)+4)
	for name, enabled := range map[string]bool{
		"datadog":    c.Datadog != nil && c.Datadog.Enabled,
		"prometheus": c.Prometheus != nil && c.Prometheus.Enabled,
		"cloudwatch": c.CloudWatch != nil && c.CloudWatch.Enabled,
		"otlp":       c.OTLP != nil && c.OTLP.Enabled,
	} {
		if enabled {
			seen[name] = struct{}{}
		}
	}
	for i, e := range c.Exporters {
		if e == nil {
			return fmt.Errorf("%w: custom exporter %d is nil", ErrInvalidConfig, i)
		}
		name := e.Name()
		if _, dup := seen[name]; dup {
			return fmt.Errorf("%w: duplicate exporter name %q", ErrInvalidConfig, name)
		}
		seen[name] = struct{}{}
	}
	return nil
}
