package stats

import (
	"time"

	"github.com/convoy-road-trips-app/stats/models"
	"go.opentelemetry.io/otel/attribute"
)

// Re-export types from models package for backwards compatibility
type (
	Metric     = models.Metric
	MetricType = models.MetricType
)

// Re-export constants
const (
	MetricTypeCounter   = models.MetricTypeCounter
	MetricTypeGauge     = models.MetricTypeGauge
	MetricTypeHistogram = models.MetricTypeHistogram
)

// Re-export functions
var (
	AcquireMetric = models.AcquireMetric
	ReleaseMetric = models.ReleaseMetric
)

// MetricOption is a function that configures a metric
type MetricOption func(*Metric)

// WithAttribute adds an attribute to the metric
func WithAttribute(key, value string) MetricOption {
	return func(m *Metric) {
		m.Attributes = append(m.Attributes, attribute.String(key, value))
	}
}

// WithAttributes adds multiple attributes to the metric
func WithAttributes(attrs map[string]string) MetricOption {
	return func(m *Metric) {
		for k, v := range attrs {
			m.Attributes = append(m.Attributes, attribute.String(k, v))
		}
	}
}

// WithPriority sets the priority of the metric
func WithPriority(priority int) MetricOption {
	return func(m *Metric) {
		m.Priority = priority
	}
}

// WithTimestamp sets the timestamp of the metric
func WithTimestamp(ts time.Time) MetricOption {
	return func(m *Metric) {
		m.Timestamp = ts
	}
}
