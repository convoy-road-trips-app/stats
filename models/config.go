package models

import (
	"fmt"
	"net"
	"strconv"
	"strings"
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
	// Endpoint, when set, overrides AgentHost and AgentPort. It accepts
	// "host:port" and "udp://host:port" (UDP), or "unixgram:///abs/path"
	// (Unix datagram socket, unavailable on Windows).
	Endpoint string
	// BufferSize is the maximum size in bytes of one datagram. Whole
	// serialized lines are batched into datagrams of at most this size; a
	// single line larger than BufferSize is dropped and reported as an export
	// error. Zero selects DefaultDatadogUDPBufferSize for UDP and
	// DefaultDatadogUnixgramBufferSize for unixgram. The maximum is
	// MaxDatadogBufferSize.
	BufferSize int
	Tags       []string
	// UseDistributions sends every histogram as a Datadog distribution
	// ("|d") instead of a histogram ("|h").
	UseDistributions bool
	// DistributionPrefixes lists metric name prefixes. A histogram whose full
	// metric name (including any client prefix) starts with one of them is
	// sent as a distribution ("|d"); other histograms stay "|h". Unlike the
	// segmentio datadog client, which matched individual field names, the
	// whole metric name is matched. An empty prefix matches every name.
	// Counters and gauges are never affected. UseDistributions takes
	// precedence when set.
	DistributionPrefixes []string
}

const (
	// DefaultDatadogUDPBufferSize is the default datagram size for UDP, which
	// fits a standard 1500-byte Ethernet MTU.
	DefaultDatadogUDPBufferSize = 1432
	// DefaultDatadogUnixgramBufferSize is the default datagram size for unixgram.
	DefaultDatadogUnixgramBufferSize = 8192
	// MaxDatadogBufferSize is the largest datagram a UDP payload can carry.
	MaxDatadogBufferSize = 65507

	endpointSchemeUDP      = "udp://"
	endpointSchemeUnixgram = "unixgram://"
)

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

// Address returns the address the Datadog exporter dials: the host:port or
// socket path of Endpoint when it is set, otherwise AgentHost:AgentPort. An
// Endpoint that does not parse is returned as is; Validate reports it.
func (c *DatadogConfig) Address() string {
	if c.Endpoint != "" {
		if _, address, err := c.ResolveEndpoint(); err == nil {
			return address
		}
		return c.Endpoint
	}
	return fmt.Sprintf("%s:%d", c.AgentHost, c.AgentPort)
}

// ResolveEndpoint returns the network ("udp" or "unixgram") and address to
// dial. Endpoint takes precedence over AgentHost and AgentPort.
func (c *DatadogConfig) ResolveEndpoint() (network, address string, err error) {
	switch {
	case c.Endpoint == "":
		return "udp", fmt.Sprintf("%s:%d", c.AgentHost, c.AgentPort), nil
	case strings.HasPrefix(c.Endpoint, endpointSchemeUnixgram):
		path := strings.TrimPrefix(c.Endpoint, endpointSchemeUnixgram)
		if len(path) < 2 || path[0] != '/' {
			return "", "", fmt.Errorf("datadog: unixgram endpoint %q needs an absolute path (unixgram:///abs/path)", c.Endpoint)
		}
		return "unixgram", path, nil
	case strings.HasPrefix(c.Endpoint, endpointSchemeUDP):
		address = strings.TrimPrefix(c.Endpoint, endpointSchemeUDP)
	case strings.Contains(c.Endpoint, "://"):
		return "", "", fmt.Errorf("datadog: unsupported endpoint scheme in %q (want host:port, udp://host:port or unixgram:///abs/path)", c.Endpoint)
	default:
		address = c.Endpoint
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", "", fmt.Errorf("datadog: invalid endpoint %q: %w", c.Endpoint, err)
	}
	if host == "" {
		return "", "", fmt.Errorf("datadog: endpoint %q has no host", c.Endpoint)
	}
	if n, err := strconv.Atoi(port); err != nil || n <= 0 || n > 65535 {
		return "", "", fmt.Errorf("datadog: endpoint %q has an invalid port", c.Endpoint)
	}
	return "udp", address, nil
}

// PacketSize returns the effective maximum datagram size: BufferSize, or the
// default for the resolved network when BufferSize is zero.
func (c *DatadogConfig) PacketSize() int {
	if c.BufferSize > 0 {
		return c.BufferSize
	}
	if network, _, err := c.ResolveEndpoint(); err == nil && network == "unixgram" {
		return DefaultDatadogUnixgramBufferSize
	}
	return DefaultDatadogUDPBufferSize
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
	if c.Endpoint != "" {
		if _, _, err := c.ResolveEndpoint(); err != nil {
			return err
		}
	} else {
		if c.AgentHost == "" {
			return fmt.Errorf("datadog: agent host is required")
		}
		if c.AgentPort <= 0 || c.AgentPort > 65535 {
			return fmt.Errorf("datadog: invalid agent port")
		}
	}
	if c.BufferSize < 0 || c.BufferSize > MaxDatadogBufferSize {
		return fmt.Errorf("datadog: buffer size %d out of range (0 for default, max %d)", c.BufferSize, MaxDatadogBufferSize)
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
