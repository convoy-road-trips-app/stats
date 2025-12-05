package serializers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/convoy-road-trips-app/stats/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

func TestEMFSerializer_Serialize(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		namespace string
		region    string
		metrics   []*models.Metric
		validate  func(t *testing.T, packets [][]byte)
	}{
		{
			name:      "counter without dimensions",
			namespace: "TestApp",
			region:    "us-east-1",
			metrics: []*models.Metric{
				{
					Name:      "http.requests",
					Type:      models.MetricTypeCounter,
					Value:     1.0,
					Timestamp: now,
				},
			},
			validate: func(t *testing.T, packets [][]byte) {
				require.Len(t, packets, 1)

				var emf map[string]interface{}
				err := json.Unmarshal(packets[0], &emf)
				require.NoError(t, err)

				assert.Equal(t, 1.0, emf["http.requests"])

				aws := emf["_aws"].(map[string]interface{})
				assert.NotNil(t, aws)

				metrics := aws["CloudWatchMetrics"].([]interface{})
				require.Len(t, metrics, 1)

				metricDef := metrics[0].(map[string]interface{})
				assert.Equal(t, "TestApp", metricDef["Namespace"])
			},
		},
		{
			name:      "gauge with dimensions",
			namespace: "TestApp",
			region:    "us-west-2",
			metrics: []*models.Metric{
				{
					Name:      "memory.usage",
					Type:      models.MetricTypeGauge,
					Value:     75.5,
					Timestamp: now,
					Attributes: []attribute.KeyValue{
						attribute.String("host", "web-1"),
						attribute.String("env", "prod"),
					},
				},
			},
			validate: func(t *testing.T, packets [][]byte) {
				require.Len(t, packets, 1)

				var emf map[string]interface{}
				err := json.Unmarshal(packets[0], &emf)
				require.NoError(t, err)

				assert.Equal(t, 75.5, emf["memory.usage"])
				assert.Equal(t, "web-1", emf["host"])
				assert.Equal(t, "prod", emf["env"])
			},
		},
		{
			name:      "histogram with unit",
			namespace: "TestApp",
			region:    "us-east-1",
			metrics: []*models.Metric{
				{
					Name:      "response.time",
					Type:      models.MetricTypeHistogram,
					Value:     123.45,
					Timestamp: now,
				},
			},
			validate: func(t *testing.T, packets [][]byte) {
				require.Len(t, packets, 1)

				var emf map[string]interface{}
				err := json.Unmarshal(packets[0], &emf)
				require.NoError(t, err)

				aws := emf["_aws"].(map[string]interface{})
				metrics := aws["CloudWatchMetrics"].([]interface{})
				metricDef := metrics[0].(map[string]interface{})
				metricsList := metricDef["Metrics"].([]interface{})
				metricInfo := metricsList[0].(map[string]interface{})

				assert.Equal(t, "Milliseconds", metricInfo["Unit"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serializer := NewEMFSerializer(tt.namespace, tt.region)
			packets, err := serializer.Serialize(tt.metrics)
			require.NoError(t, err)

			tt.validate(t, packets)
		})
	}
}

func TestEMFSerializer_Name(t *testing.T) {
	serializer := NewEMFSerializer("test", "us-east-1")
	assert.Equal(t, "emf", serializer.Name())
}

func BenchmarkEMFSerializer(b *testing.B) {
	metrics := []*models.Metric{
		{
			Name:      "http.requests",
			Type:      models.MetricTypeCounter,
			Value:     1.0,
			Timestamp: time.Now(),
			Attributes: []attribute.KeyValue{
				attribute.String("method", "GET"),
				attribute.String("status", "200"),
				attribute.String("endpoint", "/api/users"),
			},
		},
	}

	serializer := NewEMFSerializer("BenchmarkApp", "us-east-1")

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := serializer.Serialize(metrics)
		if err != nil {
			b.Fatal(err)
		}
	}
}
