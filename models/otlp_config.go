package models

import (
	"fmt"
	"math"
	"time"

	"go.opentelemetry.io/otel/attribute"
)

// OTLPProtocol selects the transport for the OTLP exporter.
type OTLPProtocol string

const (
	// OTLPProtocolGRPC selects gRPC transport (port 4317)
	OTLPProtocolGRPC OTLPProtocol = "grpc"
	// OTLPProtocolHTTP selects HTTP/protobuf transport (port 4318)
	OTLPProtocolHTTP OTLPProtocol = "http"
)

// Temporality selects whether OTLP sums and histograms are exported as cumulative or delta.
type Temporality string

const (
	// Cumulative exports values accumulated since the start of the process.
	Cumulative Temporality = "cumulative"
	// Delta exports values accumulated since the previous collection.
	Delta Temporality = "delta"
)

// OTLPConfig configures the OTLP exporter
type OTLPConfig struct {
	Enabled               bool
	Endpoint              string            // e.g., "localhost:4317" for gRPC, "localhost:4318" for HTTP
	Insecure              bool              // Use insecure connection (no TLS)
	Headers               map[string]string // Additional headers sent with each request
	ServiceName           string
	DeploymentEnvironment string
	ServiceVersion        string
	ResourceAttributes    []attribute.KeyValue
	ResourceSchemaURL     string        // Schema URL of the exported resource, e.g. semconv.SchemaURL; empty by default
	Temporality           Temporality   // "cumulative" (default) or "delta"
	Protocol              OTLPProtocol  // "grpc" (default) or "http"
	Compression           string        // "gzip" or "none"; empty means the transport default
	ExportTimeout         time.Duration // Per-export deadline; defaults to 10s if zero
	HistogramBuckets      []float64     // Explicit histogram bounds; defaults to the D9 seconds buckets when nil
	// BucketsByName holds explicit histogram bounds per metric name; an entry
	// overrides HistogramBuckets for that metric. NewClient fills it from
	// WithHistogramBucketsFor. Bounds are in the units you record in.
	BucketsByName map[string][]float64
	Retry         *OTLPRetry // Retry policy for retryable export failures; nil keeps the SDK default
}

// OTLPOverrides records the OTLP settings a caller stated explicitly through
// options. A nil pointer means "not stated", so the environment (or the
// default) applies; a non-nil pointer always wins, even when it points at a
// zero value (Insecure=false) or an empty map (Headers cleared).
type OTLPOverrides struct {
	Endpoint    *string
	Insecure    *bool
	Headers     *map[string]string
	Timeout     *time.Duration
	Compression *string
	Protocol    *OTLPProtocol
	Temporality *Temporality
}

// OTLPRetry is an exponential-backoff policy for retryable OTLP export
// failures. Every export is also bounded by its context and ExportTimeout.
type OTLPRetry struct {
	InitialInterval time.Duration // Wait after the first failure
	MaxInterval     time.Duration // Upper bound for a single wait
	MaxElapsedTime  time.Duration // Total retry budget per export; must be positive
}

// Validate rejects retry policies that could wait forever or never back off.
func (r *OTLPRetry) Validate() error {
	switch {
	case r.InitialInterval <= 0:
		return fmt.Errorf("retry initial interval must be positive")
	case r.MaxInterval < r.InitialInterval:
		return fmt.Errorf("retry max interval must not be less than the initial interval")
	case r.MaxElapsedTime <= 0:
		return fmt.Errorf("retry max elapsed time must be positive")
	}
	return nil
}

// DefaultHistogramBuckets returns the default explicit histogram bounds in seconds.
func DefaultHistogramBuckets() []float64 {
	return []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
}

// ValidateHistogramBuckets checks that bounds are non-empty, finite and
// strictly increasing.
func ValidateHistogramBuckets(bounds []float64) error {
	if len(bounds) == 0 {
		return fmt.Errorf("histogram buckets must not be empty")
	}
	for i, bound := range bounds {
		if math.IsNaN(bound) || math.IsInf(bound, 0) || (i > 0 && bound <= bounds[i-1]) {
			return fmt.Errorf("histogram buckets must be strictly increasing")
		}
	}
	return nil
}

// BucketsFor returns the histogram bounds for a metric name: the per-name
// entry, then the global bounds, then DefaultHistogramBuckets(). Bounds use
// the units the metric is recorded in.
func BucketsFor(byName map[string][]float64, global []float64, name string) []float64 {
	if bounds := byName[name]; len(bounds) > 0 {
		return bounds
	}
	if len(global) > 0 {
		return global
	}
	return DefaultHistogramBuckets()
}

// Validate validates the OTLP configuration
func (c *OTLPConfig) Validate() error {
	if c.Temporality != "" && c.Temporality != Cumulative && c.Temporality != Delta {
		return fmt.Errorf("unsupported temporality %q (use %q or %q)", c.Temporality, Cumulative, Delta)
	}
	switch c.Compression {
	case "", "gzip", "none":
	default:
		return fmt.Errorf("unsupported compression %q (use \"gzip\" or \"none\")", c.Compression)
	}
	if c.ExportTimeout < 0 {
		return fmt.Errorf("export timeout must not be negative")
	}
	if c.HistogramBuckets != nil {
		if err := ValidateHistogramBuckets(c.HistogramBuckets); err != nil {
			return err
		}
	}
	for name, bounds := range c.BucketsByName {
		if err := ValidateHistogramBuckets(bounds); err != nil {
			return fmt.Errorf("histogram buckets for %q: %w", name, err)
		}
	}
	if c.Retry != nil {
		if err := c.Retry.Validate(); err != nil {
			return err
		}
	}
	if !c.Enabled {
		return nil
	}

	if c.Endpoint == "" {
		return fmt.Errorf("endpoint is required")
	}

	switch c.Protocol {
	case "", OTLPProtocolGRPC, OTLPProtocolHTTP:
	default:
		return fmt.Errorf("unsupported protocol %q (use %q or %q)", c.Protocol, OTLPProtocolGRPC, OTLPProtocolHTTP)
	}
	return nil
}

// Address returns the full address
func (c *OTLPConfig) Address() string {
	return c.Endpoint
}
