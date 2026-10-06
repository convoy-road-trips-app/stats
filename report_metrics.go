package stats

import (
	"context"
	"reflect"
)

// MakeMetrics converts v into metrics without recording them, the way Report
// would: v and its struct tags follow Report's rules, and opts are applied to
// every metric after the struct's tags. Names carry no client prefix and no
// context tags, because no client is involved.
//
// The returned metrics are fresh values, not taken from the metric pool, so the
// caller owns them: keep them, change them, Clone them, or hand them to
// Recorder.RecordMetric (which takes ownership) later. Metrics come in the
// order Report would record them. A nil v returns no metrics; an unsupported
// field or value returns an error wrapping ErrUnsupportedReportField and no
// metrics.
func MakeMetrics(v any, opts ...MetricOption) ([]*Metric, error) {
	var c metricCollector
	if err := reportValue(context.Background(), &c, reflect.ValueOf(v), opts, 0); err != nil {
		return nil, err
	}
	return c.metrics, nil
}

// metricCollector is a valueRecorder that keeps what Report records.
type metricCollector struct {
	metrics []*Metric
}

func (c *metricCollector) add(typ MetricType, name string, value float64, opts []MetricOption) error {
	m := &Metric{Name: name, Type: typ, Value: value, Priority: 1}
	for _, opt := range opts {
		opt(m)
	}
	c.metrics = append(c.metrics, m)
	return nil
}

func (c *metricCollector) Counter(_ context.Context, name string, value float64, opts ...MetricOption) error {
	return c.add(MetricTypeCounter, name, value, opts)
}

func (c *metricCollector) Gauge(_ context.Context, name string, value float64, opts ...MetricOption) error {
	return c.add(MetricTypeGauge, name, value, opts)
}

func (c *metricCollector) Histogram(_ context.Context, name string, value float64, opts ...MetricOption) error {
	return c.add(MetricTypeHistogram, name, value, opts)
}
