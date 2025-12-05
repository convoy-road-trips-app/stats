package stats

import (
	"time"

	"github.com/convoy-road-trips-app/stats/models"
	"go.opentelemetry.io/otel/attribute"
)

// Re-export types from models package for backwards compatibility
type (
	// Metric is the metric struct
	Metric = models.Metric
	// MetricType is the metric type enum
	MetricType = models.MetricType
)

// Re-export constants
const (
	// MetricTypeCounter is the counter metric type
	MetricTypeCounter = models.MetricTypeCounter
	// MetricTypeGauge is the gauge metric type
	MetricTypeGauge = models.MetricTypeGauge
	// MetricTypeHistogram is the histogram metric type
	MetricTypeHistogram = models.MetricTypeHistogram
)

// Re-export functions
var (
	// AcquireMetric acquires a metric from the pool
	AcquireMetric = models.AcquireMetric
	// ReleaseMetric releases a metric back to the pool
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
