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
	resource      *resource.Resource
	clientOptions []stats.Option

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
	clientOptions := []stats.Option{stats.WithOTelMode()}
	mp := &MeterProvider{
		resource:      resource.Empty(),
		clientOptions: clientOptions,
		meters:        make(map[string]*Meter),
	}

	// Apply options
	for _, opt := range opts {
		if err := opt(mp); err != nil {
			return nil, err
		}
	}
	if err := mp.replaceClient(); err != nil {
		return nil, err
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

// ForceFlush exports every observation recorded before the call and returns
// once it has been exported, or with ctx's error once ctx is done.
func (mp *MeterProvider) ForceFlush(ctx context.Context) error {
	return mp.client.Flush(ctx)
}

// WithResource returns a MeterProviderOption that configures the resource
func WithResource(res *resource.Resource) MeterProviderOption {
	return func(mp *MeterProvider) error {
		if res == nil {
			return fmt.Errorf("meter resource is nil")
		}
		merged, err := resource.Merge(mp.resource, res)
		if err != nil {
			return fmt.Errorf("merge meter resource: %w", err)
		}
		mp.resource = merged
		return nil
	}
}

// WithStatsOptions returns a MeterProviderOption that configures the underlying stats client
func WithStatsOptions(opts ...stats.Option) MeterProviderOption {
	return func(mp *MeterProvider) error {
		mp.clientOptions = append(mp.clientOptions, opts...)
		return nil
	}
}

func (mp *MeterProvider) replaceClient() error {
	clientOptions := append([]stats.Option(nil), mp.clientOptions...)
	clientOptions = append(clientOptions, stats.WithOTLPResourceAttributes(mp.resource.Attributes()...))
	client, err := stats.NewClient(clientOptions...)
	if err != nil {
		return fmt.Errorf("create stats client: %w", err)
	}
	mp.client = client
	return nil
}
