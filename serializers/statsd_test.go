package serializers

import (
	"testing"
	"time"

	"github.com/convoy-road-trips-app/stats/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

func TestStatsDSerializer_Serialize(t *testing.T) {
	tests := []struct {
		name     string
		prefix   string
		metrics  []*models.Metric
		expected []string
	}{
		{
			name: "counter without prefix",
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
			name:   "counter with prefix",
			prefix: "myapp",
			metrics: []*models.Metric{
				{
					Name:  "requests",
					Type:  models.MetricTypeCounter,
					Value: 1.0,
				},
			},
			expected: []string{"myapp.requests:1|c"},
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
			expected: []string{"http.requests.method_GET.status_200:1|c"},
		},
		{
			name: "gauge",
			metrics: []*models.Metric{
				{
					Name:  "memory.usage",
					Type:  models.MetricTypeGauge,
					Value: 75.5,
				},
			},
			expected: []string{"memory.usage:75.5|g"},
		},
		{
			name: "histogram becomes ms",
			metrics: []*models.Metric{
				{
					Name:  "response.time",
					Type:  models.MetricTypeHistogram,
					Value: 123.45,
				},
			},
			expected: []string{"response.time:123.45|ms"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serializer := NewStatsDSerializer(tt.prefix)
			packets, err := serializer.Serialize(tt.metrics)
			require.NoError(t, err)
			require.Len(t, packets, len(tt.expected))

			for i, expected := range tt.expected {
				assert.Equal(t, expected, string(packets[i]))
			}
		})
	}
}

func TestStatsDSerializer_Name(t *testing.T) {
	serializer := NewStatsDSerializer("")
	assert.Equal(t, "statsd", serializer.Name())
}

func BenchmarkStatsDSerializer(b *testing.B) {
	metrics := []*models.Metric{
		{
			Name:      "http.requests",
			Type:      models.MetricTypeCounter,
			Value:     1.0,
			Timestamp: time.Now(),
			Attributes: []attribute.KeyValue{
				attribute.String("method", "GET"),
				attribute.String("status", "200"),
			},
		},
	}

	serializer := NewStatsDSerializer("myapp")

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := serializer.Serialize(metrics)
		if err != nil {
			b.Fatal(err)
		}
	}
}
