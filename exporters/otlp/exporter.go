package otlp

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
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

// ExportTimeout is the per-export deadline: the configured ExportTimeout, or 10s.
func (e *Exporter) ExportTimeout() time.Duration {
	return exportTimeout(e.config)
}

// Export sends metrics to OTLP collector. With cumulative temporality the
// export also repeats every known series not observed in metrics.
func (e *Exporter) Export(ctx context.Context, metrics []*models.Metric) error {
	if len(metrics) == 0 {
		return nil
	}
	return e.export(ctx, metrics)
}

// ExportIdle repeats the cumulative state of every known series for an export
// interval in which nothing was observed, as the OTel SDK does on each
// collection. It sends nothing with delta temporality or before the first
// observation.
func (e *Exporter) ExportIdle(ctx context.Context) error {
	return e.export(ctx, nil)
}

func (e *Exporter) export(ctx context.Context, metrics []*models.Metric) error {
	if !e.config.Enabled {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, e.ExportTimeout())
	defer cancel()

	rm := toResourceMetricsWithConfig(e.config, metrics, e.config.HistogramBuckets)
	e.mu.Lock()
	defer e.mu.Unlock()
	next := e.accumulate(&rm, time.Now())
	if len(metrics) == 0 && len(rm.ScopeMetrics[0].Metrics) == 0 {
		return nil
	}
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

// toResourceMetricsWithConfig converts metrics to OTLP data. Each histogram
// uses config.BucketsByName for its name, then an exponential histogram when
// config.ExponentialHistogram is set, then globalBounds, then the defaults.
func toResourceMetricsWithConfig(config *models.OTLPConfig, metrics []*models.Metric, globalBounds []float64) metricdata.ResourceMetrics {
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

	histograms := explicitHistograms(config, metrics, globalBounds)
	exponential := exponentialHistograms(config, metrics)
	addedHistograms := make(map[string]struct{}, len(histograms)+len(exponential))

	for _, m := range metrics {
		attrs := attribute.NewSet(slices.Clone(m.Attributes)...)
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
			addedHistograms[m.Name] = struct{}{}
			if histogram, ok := exponential[m.Name]; ok {
				metricData.Data = histogram
			} else {
				metricData.Data = histograms[m.Name]
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

// usesExponential reports whether the histogram name is exported as an
// exponential histogram: config.ExponentialHistogram is set and the name has
// no explicit buckets of its own.
func usesExponential(config *models.OTLPConfig, name string) bool {
	return config.ExponentialHistogram != nil && len(config.BucketsByName[name]) == 0
}

// exponentialHistograms aggregates the observations of the histograms that
// usesExponential selects into metricdata.ExponentialHistogram values by name,
// with a datapoint per attribute set in the order the sets first appear.
func exponentialHistograms(config *models.OTLPConfig, metrics []*models.Metric) map[string]metricdata.ExponentialHistogram[float64] {
	if config.ExponentialHistogram == nil {
		return nil
	}
	settings := config.ExponentialHistogram.Resolved()
	seriesByName := make(map[string][]*expoSeries)
	seriesByKey := make(map[histogramKey]*expoSeries)
	for _, m := range metrics {
		if m.Type != models.MetricTypeHistogram || !usesExponential(config, m.Name) {
			continue
		}
		attrs := attribute.NewSet(slices.Clone(m.Attributes)...)
		key := histogramKey{name: m.Name, attributes: attrs.Equivalent()}
		series, exists := seriesByKey[key]
		if !exists {
			series = newExpoSeries(attrs, settings.MaxSize, settings.MaxScale)
			seriesByKey[key] = series
			seriesByName[m.Name] = append(seriesByName[m.Name], series)
		}
		series.record(m)
	}
	histograms := make(map[string]metricdata.ExponentialHistogram[float64], len(seriesByName))
	for name, all := range seriesByName {
		points := make([]metricdata.ExponentialHistogramDataPoint[float64], 0, len(all))
		for _, series := range all {
			points = append(points, series.point())
		}
		histograms[name] = metricdata.ExponentialHistogram[float64]{
			DataPoints:  points,
			Temporality: metricTemporality(config.Temporality),
		}
	}
	return histograms
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
