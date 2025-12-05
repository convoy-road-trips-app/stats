package prometheus

import (
	"context"
	"fmt"

	"github.com/convoy-road-trips-app/stats/exporters"
	"github.com/convoy-road-trips-app/stats/models"
	"github.com/convoy-road-trips-app/stats/serializers"
)

// Exporter sends metrics to Prometheus via StatsD protocol over UDP
// This sends to a StatsD exporter that Prometheus scrapes
type Exporter struct {
	*exporters.BaseExporter
	config *models.PrometheusConfig
}

// NewExporter creates a new Prometheus exporter
func NewExporter(config *models.PrometheusConfig) (*Exporter, error) {
	if config == nil {
		return nil, fmt.Errorf("prometheus config is nil")
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	// Create StatsD serializer with job as prefix
	prefix := config.Job
	if prefix == "" {
		prefix = "stats"
	}

	serializer := serializers.NewStatsDSerializer(prefix)

	// Create base exporter
	base, err := exporters.NewBaseExporter(
		"prometheus",
		config.PushgatewayAddress,
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

// Export sends metrics to Prometheus
func (e *Exporter) Export(ctx context.Context, metrics []*models.Metric) error {
	if !e.config.Enabled {
		return nil
	}
	return e.BaseExporter.Export(ctx, metrics)
}
