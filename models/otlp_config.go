package models

import "fmt"

// OTLPProtocol selects the transport for the OTLP exporter.
type OTLPProtocol string

const (
	OTLPProtocolGRPC OTLPProtocol = "grpc"
	OTLPProtocolHTTP OTLPProtocol = "http"
)

// OTLPConfig configures the OTLP exporter
type OTLPConfig struct {
	Enabled     bool
	Endpoint    string            // e.g., "localhost:4317" for gRPC, "localhost:4318" for HTTP
	Insecure    bool              // Use insecure connection (no TLS)
	Headers     map[string]string // Additional headers sent with each request
	ServiceName string
	Protocol    OTLPProtocol // "grpc" (default) or "http"
}

// Validate validates the OTLP configuration
func (c *OTLPConfig) Validate() error {
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
