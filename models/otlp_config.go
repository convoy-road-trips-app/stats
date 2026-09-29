package models

import (
	"fmt"
	"math"
	"time"
)

// OTLPProtocol selects the transport for the OTLP exporter.
type OTLPProtocol string

const (
	// OTLPProtocolGRPC selects gRPC transport (port 4317)
	OTLPProtocolGRPC OTLPProtocol = "grpc"
	// OTLPProtocolHTTP selects HTTP/protobuf transport (port 4318)
	OTLPProtocolHTTP OTLPProtocol = "http"
)

// OTLPConfig configures the OTLP exporter
type OTLPConfig struct {
	Enabled          bool
	Endpoint         string            // e.g., "localhost:4317" for gRPC, "localhost:4318" for HTTP
	Insecure         bool              // Use insecure connection (no TLS)
	Headers          map[string]string // Additional headers sent with each request
	ServiceName      string
	Protocol         OTLPProtocol  // "grpc" (default) or "http"
	ExportTimeout    time.Duration // Per-export deadline; defaults to 10s if zero
	HistogramBuckets []float64     // Explicit histogram bounds; defaults to the D9 seconds buckets when nil
}

// DefaultHistogramBuckets returns the default explicit histogram bounds in seconds.
func DefaultHistogramBuckets() []float64 {
	return []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
}

// Validate validates the OTLP configuration
func (c *OTLPConfig) Validate() error {
	if c.HistogramBuckets != nil && len(c.HistogramBuckets) == 0 {
		return fmt.Errorf("histogram buckets must not be empty")
	}
	for i, bound := range c.HistogramBuckets {
		if math.IsNaN(bound) || math.IsInf(bound, 0) || (i > 0 && bound <= c.HistogramBuckets[i-1]) {
			return fmt.Errorf("histogram buckets must be strictly increasing")
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
