package otlp

import (
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
