package cloudwatch

import (
	"context"
	"fmt"

	"github.com/convoy-road-trips-app/stats/exporters"
	"github.com/convoy-road-trips-app/stats/models"
	"github.com/convoy-road-trips-app/stats/serializers"
)

// Exporter sends metrics to CloudWatch via EMF (Embedded Metric Format) over UDP
// This sends to the CloudWatch Agent which processes EMF logs
type Exporter struct {
	*exporters.BaseExporter
	config *models.CloudWatchConfig
}

// NewExporter creates a new CloudWatch exporter
func NewExporter(config *models.CloudWatchConfig) (*Exporter, error) {
	if config == nil {
		return nil, fmt.Errorf("cloudwatch config is nil")
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	// Create EMF serializer
	region := config.Region
	if region == "" {
		region = "us-east-1" // Default region
	}

	serializer := serializers.NewEMFSerializer(config.Namespace, region)

	// Create base exporter
	base, err := exporters.NewBaseExporter(
		"cloudwatch",
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

// Export sends metrics to CloudWatch
func (e *Exporter) Export(ctx context.Context, metrics []*models.Metric) error {
	if !e.config.Enabled {
		return nil
	}
	return e.BaseExporter.Export(ctx, metrics)
}
