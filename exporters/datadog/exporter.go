package datadog

import (
	"context"
	"fmt"

	"github.com/convoy-road-trips-app/stats/models"
	"github.com/convoy-road-trips-app/stats/exporters"
	"github.com/convoy-road-trips-app/stats/serializers"
)

// Exporter sends metrics to Datadog via DogStatsD protocol over UDP
type Exporter struct {
	*exporters.BaseExporter
	config *models.DatadogConfig
}

// NewExporter creates a new Datadog exporter
func NewExporter(config *models.DatadogConfig) (*Exporter, error) {
	if config == nil {
		return nil, fmt.Errorf("datadog config is nil")
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	// Create DogStatsD serializer with global tags
	serializer := serializers.NewDogStatsDSerializer(config.Tags)

	// Create base exporter
	base, err := exporters.NewBaseExporter(
		"datadog",
		config.Address(),
		serializer,
	)
	if err != nil {
		return nil, err
	}

	return &Exporter{
		BaseExporter: base,
		config:       config,
	}, nil
}

// Export sends metrics to Datadog
func (e *Exporter) Export(ctx context.Context, metrics []*models.Metric) error {
	if !e.config.Enabled {
		return nil
	}
	return e.BaseExporter.Export(ctx, metrics)
}
