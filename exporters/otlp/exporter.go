package otlp

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"

	"github.com/convoy-road-trips-app/stats/models"
)

// Exporter sends metrics to OpenTelemetry Collector via OTLP/gRPC
type Exporter struct {
	config       *models.OTLPConfig
	otlpExporter *otlpmetricgrpc.Exporter
}

// NewExporter creates a new OTLP exporter
func NewExporter(config *models.OTLPConfig) (*Exporter, error) {
	if config == nil {
		return nil, fmt.Errorf("otlp config is nil")
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	if !config.Enabled {
		return &Exporter{config: config}, nil
	}

	opts := []otlpmetricgrpc.Option{
		otlpmetricgrpc.WithEndpoint(config.Endpoint),
	}
	if config.Insecure {
		opts = append(opts, otlpmetricgrpc.WithInsecure())
	}
	if len(config.Headers) > 0 {
		opts = append(opts, otlpmetricgrpc.WithHeaders(config.Headers))
	}

	exp, err := otlpmetricgrpc.New(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("create otlp metric grpc exporter: %w", err)
	}

	return &Exporter{
		config:       config,
		otlpExporter: exp,
	}, nil
}

// Export sends metrics to OTLP collector
func (e *Exporter) Export(ctx context.Context, metrics []*models.Metric) error {
	if !e.config.Enabled || len(metrics) == 0 {
		return nil
	}

	rm := toResourceMetrics(e.config.ServiceName, metrics)
	return e.otlpExporter.Export(ctx, &rm)
}

// toResourceMetrics converts internal metrics to OTLP ResourceMetrics
func toResourceMetrics(serviceName string, metrics []*models.Metric) metricdata.ResourceMetrics {
	resName := serviceName
	if resName == "" {
		resName = "unknown_service"
	}

	res := resource.NewWithAttributes(
		"",
		attribute.String("service.name", resName),
	)

	scopeMetrics := metricdata.ScopeMetrics{
		Scope: instrumentation.Scope{
			Name: "github.com/convoy-road-trips-app/stats",
		},
		Metrics: make([]metricdata.Metrics, 0, len(metrics)),
	}

	for _, m := range metrics {
		attrs := attribute.NewSet(m.Attributes...)
		metricData := metricdata.Metrics{
			Name: m.Name,
		}

		switch m.Type {
		case models.MetricTypeCounter:
			metricData.Data = metricdata.Sum[float64]{
				Temporality: metricdata.DeltaTemporality,
				IsMonotonic: true,
				DataPoints: []metricdata.DataPoint[float64]{
					{
						Attributes: attrs,
						Time:       m.Timestamp,
						Value:      m.Value,
					},
				},
			}
		case models.MetricTypeGauge:
			metricData.Data = metricdata.Gauge[float64]{
				DataPoints: []metricdata.DataPoint[float64]{
					{
						Attributes: attrs,
						Time:       m.Timestamp,
						Value:      m.Value,
					},
				},
			}
		case models.MetricTypeHistogram:
			// For this proof of concept, we represent each observation as a single-count point
			metricData.Data = metricdata.Histogram[float64]{
				Temporality: metricdata.DeltaTemporality,
				DataPoints: []metricdata.HistogramDataPoint[float64]{
					{
						Attributes: attrs,
						Time:       m.Timestamp,
						Count:      1,
						Sum:        m.Value,
					},
				},
			}
		}

		scopeMetrics.Metrics = append(scopeMetrics.Metrics, metricData)
	}

	return metricdata.ResourceMetrics{
		Resource:     res,
		ScopeMetrics: []metricdata.ScopeMetrics{scopeMetrics},
	}
}

// Name returns the exporter name
func (e *Exporter) Name() string {
	return "otlp"
}

// Shutdown closes the exporter
func (e *Exporter) Shutdown(ctx context.Context) error {
	if e.otlpExporter == nil {
		return nil
	}
	return e.otlpExporter.Shutdown(ctx)
}
