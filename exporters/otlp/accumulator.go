package otlp

import (
	"time"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/convoy-road-trips-app/stats/models"
)

type seriesState struct {
	start     time.Time
	lastTime  time.Time
	sum       float64
	histogram metricdata.HistogramDataPoint[float64]
}

// accumulate builds the next state without committing it until transport succeeds.
// Export holds the lock across this call and the send, preserving per-series order.
func (e *Exporter) accumulate(rm *metricdata.ResourceMetrics) map[histogramKey]seriesState {
	next := make(map[histogramKey]seriesState, len(e.series))
	for key, state := range e.series {
		next[key] = state
	}
	cumulative := e.config.Temporality != models.Delta
	metrics := rm.ScopeMetrics[0].Metrics
	merged := make([]metricdata.Metrics, 0, len(metrics))
	sumIndexes := make(map[string]int)
	for _, m := range metrics {
		switch data := m.Data.(type) {
		case metricdata.Sum[float64]:
			observations := data.DataPoints
			index, exists := sumIndexes[m.Name]
			if !exists {
				index = len(merged)
				sumIndexes[m.Name] = index
				data.DataPoints = nil
				merged = append(merged, metricdata.Metrics{Name: m.Name, Data: data})
			}
			sum := merged[index].Data.(metricdata.Sum[float64])
			for _, point := range observations {
				key := histogramKey{name: m.Name, attributes: point.Attributes.Equivalent()}
				state, exists := next[key]
				if !exists {
					state.start = point.Time
				}
				state.sum += point.Value
				point.StartTime = state.start
				if !cumulative && !state.lastTime.IsZero() {
					point.StartTime = state.lastTime
				}
				if point.Time.After(state.lastTime) {
					state.lastTime = point.Time
				}
				next[key] = state
				if cumulative {
					point.Value = state.sum
				} else {
					state.sum = 0
					next[key] = state
				}
				addSumPoint(&sum, point)
			}
			merged[index].Data = sum
		case metricdata.Histogram[float64]:
			for i := range data.DataPoints {
				point := &data.DataPoints[i]
				key := histogramKey{name: m.Name, attributes: point.Attributes.Equivalent()}
				state, exists := next[key]
				if !exists {
					state.start = point.Time
				}
				if cumulative && exists {
					point.Count += state.histogram.Count
					point.Sum += state.histogram.Sum
					for bucket := range point.BucketCounts {
						point.BucketCounts[bucket] += state.histogram.BucketCounts[bucket]
					}
					if old, ok := state.histogram.Min.Value(); ok {
						if current, _ := point.Min.Value(); old < current {
							point.Min = metricdata.NewExtrema(old)
						}
					}
					if old, ok := state.histogram.Max.Value(); ok {
						if current, _ := point.Max.Value(); old > current {
							point.Max = metricdata.NewExtrema(old)
						}
					}
				}
				point.StartTime = state.start
				if !cumulative && !state.lastTime.IsZero() {
					point.StartTime = state.lastTime
				}
				if point.Time.After(state.lastTime) {
					state.lastTime = point.Time
				}
				if cumulative {
					state.histogram = *point
				} else {
					state.histogram = metricdata.HistogramDataPoint[float64]{}
				}
				next[key] = state
			}
			m.Data = data
			merged = append(merged, m)
		default:
			merged = append(merged, m)
		}
	}
	rm.ScopeMetrics[0].Metrics = merged
	return next
}

func addSumPoint(sum *metricdata.Sum[float64], point metricdata.DataPoint[float64]) {
	for i := range sum.DataPoints {
		if sum.DataPoints[i].Attributes.Equivalent() == point.Attributes.Equivalent() {
			if sum.Temporality == metricdata.DeltaTemporality {
				point.Value += sum.DataPoints[i].Value
				point.StartTime = sum.DataPoints[i].StartTime
			}
			sum.DataPoints[i] = point
			return
		}
	}
	sum.DataPoints = append(sum.DataPoints, point)
}
