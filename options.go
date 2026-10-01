package stats

import (
	"time"

	"github.com/convoy-road-trips-app/stats/models"
	"go.opentelemetry.io/otel/attribute"
)

// Option is a function that configures the stats client
type Option func(*Config)

// WithServiceName sets the service name
func WithServiceName(name string) Option {
	return func(c *Config) {
		c.ServiceName = name
		if c.OTLP == nil {
			c.OTLP = &OTLPConfig{}
		}
		c.OTLP.ServiceName = name
	}
}

// WithTemporality sets the temporality used for OTLP sums and histograms.
func WithTemporality(temporality Temporality) Option {
	return func(c *Config) {
		if c.OTLP == nil {
			c.OTLP = &OTLPConfig{}
		}
		c.OTLP.Temporality = temporality
	}
}

// WithOTLPResourceAttributes merges resource attributes into the OTLP exporter configuration.
func WithOTLPResourceAttributes(attrs ...attribute.KeyValue) Option {
	return func(c *Config) {
		if c.OTLP == nil {
			c.OTLP = &OTLPConfig{}
		}
		c.OTLP.ResourceAttributes = append(c.OTLP.ResourceAttributes, attrs...)
	}
}

// WithOTLPResourceSchemaURL sets the schema URL of the OTLP resource, the
// ResourceMetrics.schema_url the collector receives.
func WithOTLPResourceSchemaURL(schemaURL string) Option {
	return func(c *Config) {
		if c.OTLP == nil {
			c.OTLP = &OTLPConfig{}
		}
		c.OTLP.ResourceSchemaURL = schemaURL
	}
}

// WithEnvironment sets the environment (e.g., "production", "staging", "development")
func WithEnvironment(env string) Option {
	return func(c *Config) {
		c.Environment = env
		if c.OTLP == nil {
			c.OTLP = &OTLPConfig{}
		}
		c.OTLP.DeploymentEnvironment = env
	}
}

// WithBufferSize sets the ring buffer size (number of metrics)
func WithBufferSize(size int) Option {
	return func(c *Config) {
		c.BufferSize = size
	}
}

// WithWorkers sets the number of worker goroutines
func WithWorkers(workers int) Option {
	return func(c *Config) {
		c.Workers = workers
	}
}

// WithFlushInterval sets how often to flush batched metrics
func WithFlushInterval(interval time.Duration) Option {
	return func(c *Config) {
		c.FlushInterval = interval
	}
}

// WithUDPTimeout sets the UDP write timeout
func WithUDPTimeout(timeout time.Duration) Option {
	return func(c *Config) {
		c.UDPTimeout = timeout
	}
}

// WithMaxMemoryBytes sets the maximum memory usage for buffering
func WithMaxMemoryBytes(bytes int64) Option {
	return func(c *Config) {
		c.MaxMemoryBytes = bytes
	}
}

// WithMaxCardinality sets the maximum distinct attribute sets (series) per
// metric name per process. Unseen series beyond the limit are dropped and
// counted in telemetry_dropped_labels_total; admitted series keep recording.
// Zero means the default of 2000; negative values are rejected.
func WithMaxCardinality(cardinality int) Option {
	return func(c *Config) {
		c.MaxCardinality = cardinality
	}
}

// WithDropStrategy sets the strategy for handling buffer overflow
func WithDropStrategy(strategy DropStrategy) Option {
	return func(c *Config) {
		c.DropStrategy = strategy
	}
}

// WithAdaptiveBatching enables or disables adaptive batching
func WithAdaptiveBatching(enabled bool) Option {
	return func(c *Config) {
		c.AdaptiveBatching = enabled
	}
}

// WithRateLimit enables rate limiting to prevent metric flooding
// rate: metrics per second (e.g., 10000 for 10k metrics/sec)
// burst: maximum burst size (e.g., 1000 for 1k burst)
// Set rate to 0 to disable rate limiting
func WithRateLimit(rate float64, burst int) Option {
	return func(c *Config) {
		c.RateLimitPerSecond = rate
		c.RateLimitBurst = burst
	}
}

// WithOTelMode enables OpenTelemetry mode
// This is used internally by the otel package
func WithOTelMode() Option {
	return func(c *Config) {
		// OTel mode is transparent - no config changes needed
		// The otel package handles the translation
	}
}

// WithCloudWatch enables and configures CloudWatch exporter
func WithCloudWatch(cfg *CloudWatchConfig) Option {
	return func(c *Config) {
		cfg.Enabled = true
		c.CloudWatch = cfg
	}
}

// WithPrometheus enables and configures Prometheus exporter
func WithPrometheus(cfg *PrometheusConfig) Option {
	return func(c *Config) {
		cfg.Enabled = true
		c.Prometheus = cfg
	}
}

// WithDatadog enables and configures Datadog exporter
func WithDatadog(cfg *DatadogConfig) Option {
	return func(c *Config) {
		cfg.Enabled = true
		c.Datadog = cfg
	}
}

// WithOTLP enables and configures OTLP exporter
func WithOTLP(cfg *OTLPConfig) Option {
	return func(c *Config) {
		if cfg == nil {
			return
		}
		if c.OTLP != nil && c.OTLP.HistogramBuckets != nil && cfg.HistogramBuckets == nil {
			cfg.HistogramBuckets = c.OTLP.HistogramBuckets
		}
		if c.OTLP != nil {
			if cfg.Temporality == "" {
				cfg.Temporality = c.OTLP.Temporality
			}
			cfg.ResourceAttributes = append(cfg.ResourceAttributes, c.OTLP.ResourceAttributes...)
			if cfg.ResourceSchemaURL == "" {
				cfg.ResourceSchemaURL = c.OTLP.ResourceSchemaURL
			}
			if cfg.ServiceName == "" {
				cfg.ServiceName = c.OTLP.ServiceName
			}
			if cfg.DeploymentEnvironment == "" {
				cfg.DeploymentEnvironment = c.OTLP.DeploymentEnvironment
			}
			if cfg.Retry == nil {
				cfg.Retry = c.OTLP.Retry
			}
		}
		cfg.Enabled = true
		c.OTLP = cfg
	}
}

// WithHistogramBuckets sets explicit OTLP histogram bounds. Values use the
// metric's units; when unset, the OTLP exporter uses the D9 seconds buckets.
func WithHistogramBuckets(bounds []float64) Option {
	return func(c *Config) {
		if c.OTLP == nil {
			c.OTLP = &OTLPConfig{}
		}
		if bounds == nil {
			c.OTLP.HistogramBuckets = nil
			return
		}
		c.OTLP.HistogramBuckets = append([]float64{}, bounds...)
	}
}

// WithOTLPRetry retries retryable OTLP export failures with exponential backoff
// from initial up to maxInterval, for at most maxElapsed per export. Retries
// also stop when the export context ends.
func WithOTLPRetry(initial, maxInterval, maxElapsed time.Duration) Option {
	return func(c *Config) {
		if c.OTLP == nil {
			c.OTLP = &OTLPConfig{}
		}
		c.OTLP.Retry = &OTLPRetry{InitialInterval: initial, MaxInterval: maxInterval, MaxElapsedTime: maxElapsed}
	}
}

// WithRuntimeMetrics enables runtime metrics collection
func WithRuntimeMetrics() Option {
	return func(c *Config) {
		if c.RuntimeMetrics == nil {
			c.RuntimeMetrics = models.DefaultRuntimeMetricsConfig()
		}
		c.RuntimeMetrics.Enabled = true
		c.RuntimeMetrics.ApplyDefaults()
	}
}
