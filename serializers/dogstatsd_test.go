package serializers

import (
	"testing"
	"time"

	"github.com/convoy-road-trips-app/stats/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

func TestDogStatsDSerializer_Serialize(t *testing.T) {
	tests := []struct {
		name     string
		metrics  []*models.Metric
		tags     []string
		expected []string
	}{
		{
			name: "counter without tags",
			metrics: []*models.Metric{
				{
					Name:  "http.requests",
					Type:  models.MetricTypeCounter,
					Value: 1.0,
				},
			},
			expected: []string{"http.requests:1|c"},
		},
		{
			name: "counter with attributes",
			metrics: []*models.Metric{
				{
					Name:  "http.requests",
					Type:  models.MetricTypeCounter,
					Value: 1.0,
					Attributes: []attribute.KeyValue{
						attribute.String("method", "GET"),
						attribute.String("status", "200"),
					},
				},
			},
			expected: []string{"http.requests:1|c|#method:GET,status:200"},
		},
		{
			name: "gauge with global tags",
			metrics: []*models.Metric{
				{
					Name:  "memory.usage",
					Type:  models.MetricTypeGauge,
					Value: 75.5,
				},
			},
			tags:     []string{"env:prod", "host:web-1"},
			expected: []string{"memory.usage:75.5|g|#env:prod,host:web-1"},
		},
		{
			name: "histogram",
			metrics: []*models.Metric{
				{
					Name:  "response.time",
					Type:  models.MetricTypeHistogram,
					Value: 123.45,
					Attributes: []attribute.KeyValue{
						attribute.String("endpoint", "/api/users"),
					},
				},
			},
			expected: []string{"response.time:123.45|h|#endpoint:/api/users"},
		},
		{
			name: "multiple metrics",
			metrics: []*models.Metric{
				{
					Name:  "counter1",
					Type:  models.MetricTypeCounter,
					Value: 1.0,
				},
				{
					Name:  "gauge1",
					Type:  models.MetricTypeGauge,
					Value: 50.0,
				},
			},
			expected: []string{
				"counter1:1|c",
				"gauge1:50|g",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serializer := NewDogStatsDSerializer(tt.tags)
			packets, err := serializer.Serialize(tt.metrics)
			require.NoError(t, err)
			require.Len(t, packets, len(tt.expected))

			for i, expected := range tt.expected {
				assert.Equal(t, expected, string(packets[i]))
			}
		})
	}
}

func TestDogStatsDSerializer_Name(t *testing.T) {
	serializer := NewDogStatsDSerializer(nil)
	assert.Equal(t, "dogstatsd", serializer.Name())
}

func BenchmarkDogStatsDSerializer(b *testing.B) {
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

	serializer := NewDogStatsDSerializer([]string{"env:prod", "app:demo"})

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := serializer.Serialize(metrics)
		if err != nil {
			b.Fatal(err)
		}
	}
}
