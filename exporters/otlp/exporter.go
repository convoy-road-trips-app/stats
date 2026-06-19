package otlp

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"

	"github.com/convoy-road-trips-app/stats/models"
)

type otlpMetricExporter interface {
	Export(ctx context.Context, rm *metricdata.ResourceMetrics) error
	Shutdown(ctx context.Context) error
}

// Exporter sends metrics to an OpenTelemetry Collector via OTLP
type Exporter struct {
	config       *models.OTLPConfig
	otlpExporter otlpMetricExporter
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

	exp, err := newTransport(config)
	if err != nil {
		return nil, err
	}

	return &Exporter{
		config:       config,
		otlpExporter: exp,
	}, nil
}

func newTransport(config *models.OTLPConfig) (otlpMetricExporter, error) {
	switch config.Protocol {
	case models.OTLPProtocolHTTP:
		return newHTTPExporter(config)
	default:
		return newGRPCExporter(config)
	}
}

func newGRPCExporter(config *models.OTLPConfig) (*otlpmetricgrpc.Exporter, error) {
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
		return nil, fmt.Errorf("create otlp grpc exporter: %w", err)
	}
	return exp, nil
}

func newHTTPExporter(config *models.OTLPConfig) (*otlpmetrichttp.Exporter, error) {
	opts := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpoint(config.Endpoint),
	}
	if config.Insecure {
		opts = append(opts, otlpmetrichttp.WithInsecure())
	}
	if len(config.Headers) > 0 {
		opts = append(opts, otlpmetrichttp.WithHeaders(config.Headers))
	}

	exp, err := otlpmetrichttp.New(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("create otlp http exporter: %w", err)
	}
	return exp, nil
}

// Export sends metrics to OTLP collector
func (e *Exporter) Export(ctx context.Context, metrics []*models.Metric) error {
	if !e.config.Enabled || len(metrics) == 0 {
		return nil
	}

	timeout := e.config.ExportTimeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	rm := toResourceMetrics(e.config.ServiceName, metrics)
	return e.otlpExporter.Export(ctx, &rm)
}

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
			metricData.Data = metricdata.Histogram[float64]{
				Temporality: metricdata.DeltaTemporality,
				DataPoints: []metricdata.HistogramDataPoint[float64]{
					{
						Attributes:   attrs,
						Time:         m.Timestamp,
						Count:        1,
						Sum:          m.Value,
						Bounds:       []float64{},
						BucketCounts: []uint64{1},
						Min:          metricdata.NewExtrema(m.Value),
						Max:          metricdata.NewExtrema(m.Value),
					},
				},
			}
		default:
			continue
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
