package otel

import (
	"context"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/resource"

	"github.com/convoy-road-trips-app/stats"
)

// MeterProvider implements the OpenTelemetry MeterProvider interface
// while using our high-performance pipeline underneath
type MeterProvider struct {
	// Underlying stats client for pipeline access
	client *stats.Client

	// Resource describes the entity producing metrics
	resource *resource.Resource

	// Meters created by this provider
	meters map[string]*Meter
	mu     sync.RWMutex

	// Shutdown coordination
	shutdownOnce sync.Once
}

// MeterProviderOption configures the MeterProvider
type MeterProviderOption func(*MeterProvider) error

// NewMeterProvider creates a new OTel MeterProvider backed by the stats library
func NewMeterProvider(opts ...MeterProviderOption) (*MeterProvider, error) {
	// Create underlying stats client with OTel mode enabled
	client, err := stats.NewClient(
		stats.WithOTelMode(),
	)
	if err != nil {
		return nil, fmt.Errorf("create stats client: %w", err)
	}

	mp := &MeterProvider{
		client:   client,
		resource: resource.Default(),
		meters:   make(map[string]*Meter),
	}

	// Apply options
	for _, opt := range opts {
		if err := opt(mp); err != nil {
			return nil, err
		}
	}

	return mp, nil
}

// Meter creates a new Meter with the given name and options
func (mp *MeterProvider) Meter(name string, opts ...metric.MeterOption) metric.Meter {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	if meter, ok := mp.meters[name]; ok {
		return meter
	}

	meter := &Meter{
		provider:    mp,
		name:        name,
		instruments: make(map[string]instrument),
	}
	mp.meters[name] = meter
	return meter
}

// Shutdown shuts down the MeterProvider and flushes any pending metrics
func (mp *MeterProvider) Shutdown(ctx context.Context) error {
	var shutdownErr error
	mp.shutdownOnce.Do(func() {
		shutdownErr = mp.client.Shutdown(ctx)
	})
	return shutdownErr
}

// ForceFlush flushes any pending metrics
func (mp *MeterProvider) ForceFlush(ctx context.Context) error {
	// Our pipeline is already async and flushes automatically
	// This is a no-op for compatibility
	return nil
}

// WithResource returns a MeterProviderOption that configures the resource
func WithResource(res *resource.Resource) MeterProviderOption {
	return func(mp *MeterProvider) error {
		mp.resource = res
		return nil
	}
}

// WithStatsOptions returns a MeterProviderOption that configures the underlying stats client
func WithStatsOptions(opts ...stats.Option) MeterProviderOption {
	return func(mp *MeterProvider) error {
		// Re-create the client with new options
		// Note: This is a bit inefficient as we're creating a client then discarding it
		// but it allows for a clean API. In a real implementation, we might want to
		// pass options to NewMeterProvider instead.
		client, err := stats.NewClient(opts...)
		if err != nil {
			return err
		}
		mp.client = client
		return nil
	}
}
