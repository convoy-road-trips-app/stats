package otlp

import (
	"maps"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/convoy-road-trips-app/stats/models"
)

type seriesState struct {
	start     time.Time
	lastTime  time.Time
	sum       float64
	histogram metricdata.HistogramDataPoint[float64]

	// Identity of the series, kept so a cumulative export can repeat it in
	// an interval without observations.
	meta       seriesMeta
	attributes attribute.Set
	kind       seriesKind
	gauge      float64   // value of the newest observation of a gauge series
	gaugeTime  time.Time // observation time of gauge; batches can arrive out of order
}

// minPointSpacing keeps successive cumulative points of a series in distinct
// milliseconds, the timestamp resolution of Prometheus and Mimir.
const minPointSpacing = time.Millisecond

// pointTime records the next point of the series and returns its timestamp.
// Workers export batches concurrently, so a later export can hold observations
// older than the previously exported point. A delta point starts where that
// point ended, so its Time is raised to at least that end. Backends drop a
// larger cumulative total stamped at or before the previous point as a
// duplicate or stale sample, so a cumulative point is moved 1 ms past it.
// Observations of the same batch are clamped to the same floor and therefore
// do not drift. Within a batch the time never goes back, because a sum
// datapoint merged from several observations takes the time of the last one
// and must cover the newest observation it includes.
func (s *seriesState) pointTime(observed, exported time.Time, cumulative bool) time.Time {
	floor := exported
	if cumulative && !exported.IsZero() {
		floor = exported.Add(minPointSpacing)
	}
	if observed.Before(floor) {
		observed = floor
	}
	if observed.Before(s.lastTime) {
		observed = s.lastTime
	}
	s.lastTime = observed
	return observed
}

// accumulation builds the series state of one export on top of the state
// committed by the previous successful export.
type accumulation struct {
	exported   seriesStates
	next       seriesStates
	cumulative bool
	seen       map[histogramKey]struct{} // series observed in this export
}

// accumulate builds the next state without committing it until transport succeeds.
// Export holds the lock across this call and the send, preserving per-series order.
func (e *Exporter) accumulate(rm *metricdata.ResourceMetrics, now time.Time) seriesStates {
	var exported seriesStates
	if e.series != nil {
		exported = *e.series
	}
	acc := accumulation{
		exported:   exported,
		next:       make(seriesStates, len(exported)),
		cumulative: e.config.Temporality != models.Delta,
		seen:       make(map[histogramKey]struct{}),
	}
	maps.Copy(acc.next, exported)
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
				m.Data = data
				merged = append(merged, m)
			}
			sum := merged[index].Data.(metricdata.Sum[float64])
			for _, point := range observations {
				addSumPoint(&sum, acc.sumPoint(metaOf(m), point))
			}
			merged[index].Data = sum
		case metricdata.Histogram[float64]:
			for i := range data.DataPoints {
				acc.histogramPoint(metaOf(m), &data.DataPoints[i])
			}
			m.Data = data
			merged = append(merged, m)
		case metricdata.Gauge[float64]:
			for _, point := range data.DataPoints {
				acc.gaugePoint(metaOf(m), point)
			}
			merged = append(merged, m)
		default:
			merged = append(merged, m)
		}
	}
	merged = acc.unobserved(merged, now)
	rm.ScopeMetrics[0].Metrics = merged
	return acc.next
}

// state returns the series state of key, starting a new series at observed.
func (a *accumulation) state(key histogramKey, observed time.Time) (seriesState, bool) {
	state, exists := a.next[key]
	if !exists {
		state.start = observed
	}
	return state, exists
}

// times returns the StartTime and Time of the series' next point.
func (a *accumulation) times(key histogramKey, state *seriesState, observed time.Time) (start, at time.Time) {
	start = state.start
	if !a.cumulative && !state.lastTime.IsZero() {
		start = state.lastTime
	}
	return start, state.pointTime(observed, a.exported[key].lastTime, a.cumulative)
}

// sumPoint returns point as exported: the running total when cumulative.
func (a *accumulation) sumPoint(meta seriesMeta, point metricdata.DataPoint[float64]) metricdata.DataPoint[float64] {
	key := histogramKey{name: meta.name, attributes: point.Attributes.Equivalent()}
	state, _ := a.state(key, point.Time)
	state.meta, state.attributes, state.kind = meta, point.Attributes, kindSum
	a.seen[key] = struct{}{}
	state.sum += point.Value
	point.StartTime, point.Time = a.times(key, &state, point.Time)
	if a.cumulative {
		point.Value = state.sum
	} else {
		state.sum = 0
	}
	a.next[key] = state
	return point
}

// histogramPoint adds the series' earlier exports to point when cumulative.
func (a *accumulation) histogramPoint(meta seriesMeta, point *metricdata.HistogramDataPoint[float64]) {
	key := histogramKey{name: meta.name, attributes: point.Attributes.Equivalent()}
	state, exists := a.state(key, point.Time)
	state.meta, state.attributes, state.kind = meta, point.Attributes, kindHistogram
	a.seen[key] = struct{}{}
	if a.cumulative && exists {
		addHistogram(point, &state.histogram)
	}
	point.StartTime, point.Time = a.times(key, &state, point.Time)
	if a.cumulative {
		state.histogram = *point
		state.histogram.Exemplars = nil // exemplars belong to one export interval
	} else {
		state.histogram = metricdata.HistogramDataPoint[float64]{}
	}
	a.next[key] = state
}

// addHistogram adds the counts, sum and extrema of previous to point.
func addHistogram(point, previous *metricdata.HistogramDataPoint[float64]) {
	point.Count += previous.Count
	point.Sum += previous.Sum
	for bucket := range point.BucketCounts {
		point.BucketCounts[bucket] += previous.BucketCounts[bucket]
	}
	if old, ok := previous.Min.Value(); ok {
		if current, _ := point.Min.Value(); old < current {
			point.Min = metricdata.NewExtrema(old)
		}
	}
	if old, ok := previous.Max.Value(); ok {
		if current, _ := point.Max.Value(); old > current {
			point.Max = metricdata.NewExtrema(old)
		}
	}
}

// addSumPoint merges point into the datapoint of its series and keeps the
// sampled exemplar with the latest observation time, whatever the batch order.
func addSumPoint(sum *metricdata.Sum[float64], point metricdata.DataPoint[float64]) {
	for i := range sum.DataPoints {
		if sum.DataPoints[i].Attributes.Equivalent() == point.Attributes.Equivalent() {
			if sum.Temporality == metricdata.DeltaTemporality {
				point.Value += sum.DataPoints[i].Value
				point.StartTime = sum.DataPoints[i].StartTime
			}
			if previous := sum.DataPoints[i].Exemplars; len(previous) > 0 &&
				(len(point.Exemplars) == 0 || previous[0].Time.After(point.Exemplars[0].Time)) {
				point.Exemplars = previous
			}
			sum.DataPoints[i] = point
			return
		}
	}
	sum.DataPoints = append(sum.DataPoints, point)
}
