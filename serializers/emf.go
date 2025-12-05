package serializers

import (
	"encoding/json"
	"sync"

	"github.com/convoy-road-trips-app/stats/models"
)

// EMFSerializer serializes metrics to CloudWatch Embedded Metric Format (EMF)
// Format: JSON-based log format that CloudWatch automatically extracts metrics from
type EMFSerializer struct {
	namespace string
	region    string
}

// NewEMFSerializer creates a new EMF serializer
func NewEMFSerializer(namespace, region string) *EMFSerializer {
	return &EMFSerializer{
		namespace: namespace,
		region:    region,
	}
}

// Name returns the serializer name
func (s *EMFSerializer) Name() string {
	return "emf"
}

// Serialize converts metrics to CloudWatch EMF format
func (s *EMFSerializer) Serialize(metrics []*models.Metric) ([][]byte, error) {
	// CloudWatch EMF allows batching metrics with same dimensions
	// For simplicity, we'll create one EMF message per metric
	// In production, you might want to batch metrics with same dimensions

	packets := make([][]byte, 0, len(metrics))

	for _, metric := range metrics {
		emf := s.buildEMF(metric)
		data, err := json.Marshal(emf)
		if err != nil {
			return nil, err
		}
		packets = append(packets, data)
	}

	return packets, nil
}

// buildEMF constructs an EMF message for a single metric
func (s *EMFSerializer) buildEMF(metric *models.Metric) map[string]interface{} {
	// Build dimensions from attributes
	dimensions := make(map[string]string)
	dimensionSets := [][]string{}
	dimensionNames := []string{}

	for _, attr := range metric.Attributes {
		key := string(attr.Key)
		value := attr.Value.Emit()
		dimensions[key] = value
		dimensionNames = append(dimensionNames, key)
	}

	if len(dimensionNames) > 0 {
		dimensionSets = append(dimensionSets, dimensionNames)
	}

	// Determine unit and storage resolution based on metric type
	unit := s.getUnit(metric.Type)
	storageResolution := 60 // Standard resolution (1 minute)

	// Build metric definition
	metricDef := map[string]interface{}{
		"Namespace": s.namespace,
		"Metrics": []map[string]interface{}{
			{
				"Name":              metric.Name,
				"Unit":              unit,
				"StorageResolution": storageResolution,
			},
		},
	}

	if len(dimensionSets) > 0 {
		metricDef["Dimensions"] = dimensionSets
	}

	// Build the complete EMF message
	emf := map[string]interface{}{
		"_aws": map[string]interface{}{
			"Timestamp": metric.Timestamp.UnixMilli(),
			"CloudWatchMetrics": []map[string]interface{}{
				metricDef,
			},
		},
		metric.Name: metric.Value,
	}

	// Add dimensions as fields
	for key, value := range dimensions {
		emf[key] = value
	}

	return emf
}

// getUnit returns the CloudWatch unit for a metric type
func (s *EMFSerializer) getUnit(t models.MetricType) string {
	switch t {
	case models.MetricTypeCounter:
		return "Count"
	case models.MetricTypeGauge:
		return "None"
	case models.MetricTypeHistogram:
		return "Milliseconds"
	default:
		return "None"
	}
}

var emfPool = sync.Pool{
	New: func() any {
		return make(map[string]interface{}, 16)
	},
}
