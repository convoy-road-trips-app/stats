package otlp

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

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
	mu           sync.Mutex
	// series is the committed state per series. It is held by pointer because
	// a map field would make Exporter incomparable, an API break from v1.0.x.
	series *seriesStates
}

type seriesStates map[histogramKey]seriesState

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
	if r := config.Retry; r != nil {
		opts = append(opts, otlpmetricgrpc.WithRetry(otlpmetricgrpc.RetryConfig{
			Enabled: true, InitialInterval: r.InitialInterval, MaxInterval: r.MaxInterval, MaxElapsedTime: r.MaxElapsedTime,
		}))
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
	if r := config.Retry; r != nil {
		opts = append(opts, otlpmetrichttp.WithRetry(otlpmetrichttp.RetryConfig{
			Enabled: true, InitialInterval: r.InitialInterval, MaxInterval: r.MaxInterval, MaxElapsedTime: r.MaxElapsedTime,
		}))
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

	bounds := e.config.HistogramBuckets
	if bounds == nil {
		bounds = models.DefaultHistogramBuckets()
	}
	rm := toResourceMetricsWithConfig(e.config, metrics, bounds)
	e.mu.Lock()
	defer e.mu.Unlock()
	next := e.accumulate(&rm)
	err := e.otlpExporter.Export(ctx, &rm)
	// Keep observations even after a failed send so the next cumulative export
	// includes the interval that the collector did not receive.
	e.series = &next
	return err
}

func toResourceMetrics(serviceName string, metrics []*models.Metric) metricdata.ResourceMetrics {
	return toResourceMetricsWithBuckets(serviceName, metrics, models.DefaultHistogramBuckets())
}

type histogramKey struct {
	name       string
	attributes attribute.Distinct
}

func toResourceMetricsWithBuckets(serviceName string, metrics []*models.Metric, bounds []float64) metricdata.ResourceMetrics {
	return toResourceMetricsWithConfig(&models.OTLPConfig{ServiceName: serviceName}, metrics, bounds)
}

func toResourceMetricsWithConfig(config *models.OTLPConfig, metrics []*models.Metric, bounds []float64) metricdata.ResourceMetrics {
	res := resourceForConfig(config)
	temporality := config.Temporality
	if temporality == "" {
		temporality = models.Cumulative
	}

	scopeMetrics := metricdata.ScopeMetrics{
		Scope: instrumentation.Scope{
			Name: "github.com/convoy-road-trips-app/stats",
		},
		Metrics: make([]metricdata.Metrics, 0, len(metrics)),
	}

	histograms := make(map[string]metricdata.Histogram[float64])
	histogramIndexes := make(map[histogramKey]int)
	exemplars := bucketExemplars{}
	for _, m := range metrics {
		if m.Type != models.MetricTypeHistogram {
			continue
		}
		attrs := attribute.NewSet(m.Attributes...)
		key := histogramKey{name: m.Name, attributes: attrs.Equivalent()}
		histogram, exists := histograms[m.Name]
		if !exists {
			histogram = metricdata.Histogram[float64]{
				Temporality: metricTemporality(temporality),
			}
		}
		index, exists := histogramIndexes[key]
		if !exists {
			index = len(histogram.DataPoints)
			histogramIndexes[key] = index
			histogram.DataPoints = append(histogram.DataPoints, metricdata.HistogramDataPoint[float64]{
				Attributes:   attrs,
				Time:         m.Timestamp,
				Bounds:       append([]float64(nil), bounds...),
				BucketCounts: make([]uint64, len(bounds)+1),
				Min:          metricdata.NewExtrema(m.Value),
				Max:          metricdata.NewExtrema(m.Value),
			})
		}
		point := &histogram.DataPoints[index]
		point.Count++
		point.Sum += m.Value
		if m.Timestamp.After(point.Time) {
			point.Time = m.Timestamp
		}
		lowest, _ := point.Min.Value()
		if m.Value < lowest {
			point.Min = metricdata.NewExtrema(m.Value)
		}
		highest, _ := point.Max.Value()
		if m.Value > highest {
			point.Max = metricdata.NewExtrema(m.Value)
		}
		bucket := sort.Search(len(bounds), func(i int) bool { return m.Value <= bounds[i] })
		point.BucketCounts[bucket]++
		exemplars.offer(key, bucket, len(point.BucketCounts), m)
		histograms[m.Name] = histogram
	}
	exemplars.attach(histograms, histogramIndexes)

	addedHistograms := make(map[string]struct{}, len(histograms))

	for _, m := range metrics {
		attrs := attribute.NewSet(m.Attributes...)
		metricData := metricdata.Metrics{
			Name:        m.Name,
			Description: m.Description,
			Unit:        m.Unit,
		}

		switch m.Type {
		case models.MetricTypeCounter:
			metricData.Data = metricdata.Sum[float64]{
				Temporality: metricTemporality(temporality),
				IsMonotonic: true,
				DataPoints: []metricdata.DataPoint[float64]{
					{
						Attributes: attrs,
						Time:       m.Timestamp,
						Value:      m.Value,
						Exemplars:  exemplarOf(m),
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
			if _, exists := addedHistograms[m.Name]; exists {
				continue
			}
			metricData.Data = histograms[m.Name]
			addedHistograms[m.Name] = struct{}{}
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
