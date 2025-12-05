package models

import "fmt"

// OTLPConfig configures the OTLP exporter
type OTLPConfig struct {
	Enabled  bool
	Endpoint string // e.g., "localhost:4317" for gRPC
	Insecure bool   // Use insecure connection
	Headers  map[string]string
}

// Validate validates the OTLP configuration
func (c *OTLPConfig) Validate() error {
	if !c.Enabled {
		return nil
	}

	if c.Endpoint == "" {
		return fmt.Errorf("endpoint is required")
	}

	return nil
}

// Address returns the full address
func (c *OTLPConfig) Address() string {
	return c.Endpoint
}
