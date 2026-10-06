package otlp

import (
	"slices"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/convoy-road-trips-app/stats/models"
)

// exemplarOf returns the exemplar of an observation recorded under a sampled
// span, or nil. The ids are copied because m returns to the metric pool after
// the export and the exporter keeps cumulative state beyond it.
func exemplarOf(m *models.Metric) []metricdata.Exemplar[float64] {
	if !m.HasExemplar() {
		return nil
	}
	traceID, spanID := m.TraceID, m.SpanID
	return []metricdata.Exemplar[float64]{{
		Time:    m.Timestamp,
		Value:   m.Value,
		TraceID: traceID[:],
		SpanID:  spanID[:],
	}}
}

// bucketExemplars keeps, for each histogram datapoint of one export, the latest
// sampled exemplar per bucket, like the OTel SDK's aligned histogram bucket
// exemplar reservoir.
type bucketExemplars map[histogramKey][]metricdata.Exemplar[float64]

// offer records m as the exemplar of bucket unless that bucket already holds a
// later observation. buckets is the number of buckets of the datapoint.
func (b bucketExemplars) offer(key histogramKey, bucket, buckets int, m *models.Metric) {
	exemplar := exemplarOf(m)
	if exemplar == nil {
		return
	}
	slots, exists := b[key]
	if !exists {
		slots = make([]metricdata.Exemplar[float64], buckets)
		b[key] = slots
	}
	if slots[bucket].TraceID == nil || !m.Timestamp.Before(slots[bucket].Time) {
		slots[bucket] = exemplar[0]
	}
}

// attach sets the collected exemplars, in bucket order, on their datapoints.
func (b bucketExemplars) attach(histograms map[string]metricdata.Histogram[float64], indexes map[histogramKey]int) {
	for key, slots := range b {
		point := &histograms[key.name].DataPoints[indexes[key]]
		for _, exemplar := range slots {
			if exemplar.TraceID != nil {
				point.Exemplars = append(point.Exemplars, exemplar)
			}
		}
	}
}

// newestExemplars keeps the newest sampled exemplars of one datapoint, at most
// limit. The OTel SDK keeps a random sample of that size for exponential
// histograms; like the other datapoints of this exporter, this keeps the newest.
type newestExemplars struct {
	limit int
	kept  []metricdata.Exemplar[float64]
}

// offer keeps the exemplar of m. When full, it replaces the oldest kept
// exemplar unless m is older still.
func (e *newestExemplars) offer(m *models.Metric) {
	exemplar := exemplarOf(m)
	if exemplar == nil {
		return
	}
	if len(e.kept) < e.limit {
		e.kept = append(e.kept, exemplar[0])
		return
	}
	oldest := 0
	for i := range e.kept {
		if e.kept[i].Time.Before(e.kept[oldest].Time) {
			oldest = i
		}
	}
	if !m.Timestamp.Before(e.kept[oldest].Time) {
		e.kept[oldest] = exemplar[0]
	}
}

// sorted returns the kept exemplars, oldest first.
func (e *newestExemplars) sorted() []metricdata.Exemplar[float64] {
	slices.SortStableFunc(e.kept, func(a, b metricdata.Exemplar[float64]) int {
		return a.Time.Compare(b.Time)
	})
	return e.kept
}
