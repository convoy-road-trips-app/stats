package models

import (
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
)

// MetricType represents the type of metric
type MetricType int

const (
	MetricTypeCounter MetricType = iota
	MetricTypeGauge
	MetricTypeHistogram
)

// String returns the string representation of the metric type
func (m MetricType) String() string {
	switch m {
	case MetricTypeCounter:
		return "counter"
	case MetricTypeGauge:
		return "gauge"
	case MetricTypeHistogram:
		return "histogram"
	default:
		return "unknown"
	}
}

// Metric represents a single metric data point
type Metric struct {
	Name       string
	Type       MetricType
	Value      float64
	Attributes []attribute.KeyValue
	Timestamp  time.Time
	Priority   int // 0=low, 1=normal, 2=high, 3=critical
}

// EstimateSize returns an estimate of the metric size in bytes
func (m *Metric) EstimateSize() int64 {
	size := int64(len(m.Name))
	size += 8  // Value (float64)
	size += 8  // Timestamp
	size += 4  // Priority
	size += 4  // Type

	// Attributes
	for _, attr := range m.Attributes {
		size += int64(len(string(attr.Key)))
		size += int64(len(attr.Value.Emit()))
	}

	return size
}

// Reset clears the metric for reuse
func (m *Metric) Reset() {
	m.Name = ""
	m.Type = MetricTypeCounter
	m.Value = 0
	m.Attributes = m.Attributes[:0]
	m.Timestamp = time.Time{}
	m.Priority = 1
}

// metricPool is a sync.Pool for reusing Metric objects
var metricPool = sync.Pool{
	New: func() any {
		return &Metric{
			Attributes: make([]attribute.KeyValue, 0, 8),
			Priority:   1, // Normal priority by default
		}
	},
}

// AcquireMetric gets a metric from the pool
func AcquireMetric() *Metric {
	m := metricPool.Get().(*Metric)
	m.Reset()
	return m
}

// ReleaseMetric returns a metric to the pool
func ReleaseMetric(m *Metric) {
	if m != nil {
		metricPool.Put(m)
	}
}
