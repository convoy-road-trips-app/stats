package stats

import "time"

// Option is a function that configures the stats client
type Option func(*Config)

// WithServiceName sets the service name
func WithServiceName(name string) Option {
	return func(c *Config) {
		c.ServiceName = name
	}
}

// WithEnvironment sets the environment (e.g., "production", "staging", "development")
func WithEnvironment(env string) Option {
	return func(c *Config) {
		c.Environment = env
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

// WithMaxCardinality sets the maximum unique attribute combinations
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
