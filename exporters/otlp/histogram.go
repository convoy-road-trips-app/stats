package otlp

import (
	"slices"
	"sort"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/convoy-road-trips-app/stats/models"
)

// explicitHistograms aggregates the observations of the explicit-bucket
// histograms by name and attribute set.
func explicitHistograms(config *models.OTLPConfig, metrics []*models.Metric, globalBounds []float64) map[string]metricdata.Histogram[float64] {
	histograms := make(map[string]metricdata.Histogram[float64])
	histogramIndexes := make(map[histogramKey]int)
	exemplars := bucketExemplars{}
	for _, m := range metrics {
		if m.Type != models.MetricTypeHistogram || usesExponential(config, m.Name) {
			continue
		}
		bounds := models.BucketsFor(config.BucketsByName, globalBounds, m.Name)
		attrs := attribute.NewSet(slices.Clone(m.Attributes)...)
		key := histogramKey{name: m.Name, attributes: attrs.Equivalent()}
		histogram, exists := histograms[m.Name]
		if !exists {
			histogram = metricdata.Histogram[float64]{
				Temporality: metricTemporality(config.Temporality),
			}
		}
		index, exists := histogramIndexes[key]
		if !exists {
			index = len(histogram.DataPoints)
			histogramIndexes[key] = index
			histogram.DataPoints = append(histogram.DataPoints, metricdata.HistogramDataPoint[float64]{
				Attributes:   attrs,
				Time:         m.Timestamp,
				Bounds:       append([]float64(nil), bounds...),
				BucketCounts: make([]uint64, len(bounds)+1),
				Min:          metricdata.NewExtrema(m.Value),
				Max:          metricdata.NewExtrema(m.Value),
			})
		}
		point := &histogram.DataPoints[index]
		point.Count++
		point.Sum += m.Value
		if m.Timestamp.After(point.Time) {
			point.Time = m.Timestamp
		}
		lowest, _ := point.Min.Value()
		if m.Value < lowest {
			point.Min = metricdata.NewExtrema(m.Value)
		}
		highest, _ := point.Max.Value()
		if m.Value > highest {
			point.Max = metricdata.NewExtrema(m.Value)
		}
		bucket := sort.Search(len(bounds), func(i int) bool { return m.Value <= bounds[i] })
		point.BucketCounts[bucket]++
		exemplars.offer(key, bucket, len(point.BucketCounts), m)
		histograms[m.Name] = histogram
	}
	exemplars.attach(histograms, histogramIndexes)
	return histograms
}
