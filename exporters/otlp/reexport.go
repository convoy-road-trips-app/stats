package otlp

import (
	"slices"
	"time"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

type seriesKind int

const (
	kindSum seriesKind = iota + 1
	kindHistogram
	kindGauge
)

// seriesMeta is the name, description and unit of a series.
type seriesMeta struct {
	name, description, unit string
}

func metaOf(m metricdata.Metrics) seriesMeta {
	return seriesMeta{name: m.Name, description: m.Description, unit: m.Unit}
}

// gaugePoint remembers the last value of a gauge series.
func (a *accumulation) gaugePoint(meta seriesMeta, point metricdata.DataPoint[float64]) {
	key := histogramKey{name: meta.name, attributes: point.Attributes.Equivalent()}
	state, _ := a.state(key, point.Time)
	state.meta, state.attributes, state.kind = meta, point.Attributes, kindGauge
	state.gauge = point.Value
	if point.Time.After(state.lastTime) {
		state.lastTime = point.Time
	}
	a.seen[key] = struct{}{}
	a.next[key] = state
}

// unobserved appends to metrics a point for every cumulative series that was
// not observed in this export, stamped at now. Delta exports are unchanged.
// Series dropped by the cardinality limits never reach the exporter, so they
// have no state to repeat.
func (a *accumulation) unobserved(metrics []metricdata.Metrics, now time.Time) []metricdata.Metrics {
	if !a.cumulative {
		return metrics
	}
	for key := range a.next {
		if _, observed := a.seen[key]; observed {
			continue
		}
		state := a.next[key]
		start, at := a.times(key, &state, now)
		a.next[key] = state
		meta := state.meta
		switch state.kind {
		case kindSum:
			metrics = appendUnobserved(metrics, meta, func(m *metricdata.Metrics) {
				sum, _ := m.Data.(metricdata.Sum[float64])
				sum.Temporality, sum.IsMonotonic = metricdata.CumulativeTemporality, true
				sum.DataPoints = append(sum.DataPoints, metricdata.DataPoint[float64]{
					Attributes: state.attributes, StartTime: start, Time: at, Value: state.sum,
				})
				m.Data = sum
			})
		case kindGauge:
			metrics = appendUnobserved(metrics, meta, func(m *metricdata.Metrics) {
				gauge, _ := m.Data.(metricdata.Gauge[float64])
				gauge.DataPoints = append(gauge.DataPoints, metricdata.DataPoint[float64]{
					Attributes: state.attributes, Time: at, Value: state.gauge,
				})
				m.Data = gauge
			})
		case kindHistogram:
			point := state.histogram
			point.Bounds = slices.Clone(point.Bounds)
			point.BucketCounts = slices.Clone(point.BucketCounts)
			point.StartTime, point.Time = start, at
			metrics = appendUnobserved(metrics, meta, func(m *metricdata.Metrics) {
				hist, _ := m.Data.(metricdata.Histogram[float64])
				hist.Temporality = metricdata.CumulativeTemporality
				hist.DataPoints = append(hist.DataPoints, point)
				m.Data = hist
			})
		}
	}
	return metrics
}

// appendUnobserved applies add to the metric named meta.name, creating it when
// this export has none of that name and data type yet.
func appendUnobserved(metrics []metricdata.Metrics, meta seriesMeta, add func(*metricdata.Metrics)) []metricdata.Metrics {
	probe := metricdata.Metrics{}
	add(&probe)
	for i := range metrics {
		if metrics[i].Name == meta.name && sameKind(metrics[i].Data, probe.Data) {
			add(&metrics[i])
			return metrics
		}
	}
	probe = metricdata.Metrics{Name: meta.name, Description: meta.description, Unit: meta.unit}
	add(&probe)
	return append(metrics, probe)
}

func sameKind(a, b metricdata.Aggregation) bool {
	switch a.(type) {
	case metricdata.Sum[float64]:
		_, ok := b.(metricdata.Sum[float64])
		return ok
	case metricdata.Gauge[float64]:
		_, ok := b.(metricdata.Gauge[float64])
		return ok
	case metricdata.Histogram[float64]:
		_, ok := b.(metricdata.Histogram[float64])
		return ok
	}
	return false
}
