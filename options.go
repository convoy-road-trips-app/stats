package stats

import (
	"cmp"
	"maps"
	"slices"
	"time"

	"github.com/convoy-road-trips-app/stats/models"
	"go.opentelemetry.io/otel/attribute"
)

// ref returns a pointer to a copy of v, for OTLPOverrides fields.
func ref[T any](v T) *T { return &v }

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
		c.OTLPOverrides.Temporality = ref(temporality)
	}
}

// WithOTLPExportTimeout sets the per-export deadline of the OTLP exporter. It
// beats OTEL_EXPORTER_OTLP_TIMEOUT.
func WithOTLPExportTimeout(d time.Duration) Option {
	return func(c *Config) {
		if c.OTLP == nil {
			c.OTLP = &OTLPConfig{}
		}
		c.OTLP.ExportTimeout = d
		c.OTLPOverrides.Timeout = ref(d)
	}
}

// WithOTLPExportInterval sets how often batched metrics are handed to the
// exporters, which is how often OTLP exports. It is the same pipeline flush
// interval as WithFlushInterval, and beats OTEL_METRIC_EXPORT_INTERVAL.
func WithOTLPExportInterval(d time.Duration) Option {
	return WithFlushInterval(d)
}

// WithOTLPFromEnv enables the OTLP exporter and configures it from the
// OTEL_EXPORTER_OTLP_* environment variables. Options given after it win over
// the environment; the environment alone never enables OTLP.
func WithOTLPFromEnv() Option {
	return func(c *Config) {
		if c.OTLP == nil {
			c.OTLP = &OTLPConfig{}
		}
		c.OTLP.Enabled = true
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
		c.FlushIntervalSet = true
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

// WithExporter registers a custom exporter. It runs after the built-in
// exporters, in parallel with them, and gets its own entry in
// PipelineStats.ExporterErrors under e.Name(). NewClient fails with
// ErrInvalidConfig if e is nil or its name is already used by another exporter.
// The client shuts the exporter down on Close.
func WithExporter(e Exporter) Option {
	return func(c *Config) {
		c.Exporters = append(slices.Clone(c.Exporters), e)
	}
}

// WithOTLP enables and configures OTLP exporter. cfg is copied, so one option
// can configure several clients.
func WithOTLP(cfg *OTLPConfig) Option {
	return func(c *Config) {
		if cfg == nil {
			return
		}
		merged := *cfg
		merged.ResourceAttributes = slices.Clone(cfg.ResourceAttributes)
		merged.HistogramBuckets = slices.Clone(cfg.HistogramBuckets)
		merged.Headers = maps.Clone(cfg.Headers)
		if c.OTLP != nil {
			if merged.HistogramBuckets == nil {
				merged.HistogramBuckets = c.OTLP.HistogramBuckets
			}
			merged.Temporality = cmp.Or(merged.Temporality, c.OTLP.Temporality)
			merged.ResourceAttributes = append(merged.ResourceAttributes, c.OTLP.ResourceAttributes...)
			merged.ResourceSchemaURL = cmp.Or(merged.ResourceSchemaURL, c.OTLP.ResourceSchemaURL)
			merged.ServiceName = cmp.Or(merged.ServiceName, c.OTLP.ServiceName)
			merged.DeploymentEnvironment = cmp.Or(merged.DeploymentEnvironment, c.OTLP.DeploymentEnvironment)
			merged.Retry = cmp.Or(merged.Retry, c.OTLP.Retry)
		}
		merged.Enabled = true
		c.OTLP = &merged
		// A passed struct is a complete explicit statement: every field is
		// stated, so a zero Insecure or empty Headers beats the environment.
		c.OTLPOverrides = OTLPOverrides{
			Endpoint:    ref(merged.Endpoint),
			Insecure:    ref(merged.Insecure),
			Headers:     ref(maps.Clone(merged.Headers)),
			Timeout:     ref(merged.ExportTimeout),
			Compression: ref(merged.Compression),
			Protocol:    ref(merged.Protocol),
			Temporality: ref(merged.Temporality),
		}
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

// WithHistogramBucketsFor sets explicit histogram bounds for one metric name,
// overriding the global buckets for that metric. Bounds use the units you
// record in and must be non-empty, finite and strictly increasing; NewClient
// returns an error otherwise. The bounds are copied.
func WithHistogramBucketsFor(name string, bounds ...float64) Option {
	return func(c *Config) {
		if c.HistogramBucketsByName == nil {
			c.HistogramBucketsByName = make(map[string][]float64)
		}
		c.HistogramBucketsByName[name] = append([]float64{}, bounds...)
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

// WithRuntimeProcessMetrics enables process-level runtime metrics (CPU,
// memory, page faults, open files, threads, context switches) and implies
// WithRuntimeMetrics. They are collected on Linux; other platforms emit
// nothing extra.
func WithRuntimeProcessMetrics() Option {
	return func(c *Config) {
		WithRuntimeMetrics()(c)
		c.RuntimeMetrics.ProcessMetrics = true
	}
}
