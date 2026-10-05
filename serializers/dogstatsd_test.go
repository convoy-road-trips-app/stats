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

func histogram(name string) *models.Metric {
	return &models.Metric{Name: name, Type: models.MetricTypeHistogram, Value: 2}
}

func serializeOne(t *testing.T, s *DogStatsDSerializer, m *models.Metric) string {
	t.Helper()
	packets, err := s.Serialize([]*models.Metric{m})
	require.NoError(t, err)
	require.Len(t, packets, 1)
	return string(packets[0])
}

func TestDefaultHistogramIsH(t *testing.T) {
	s := NewDogStatsDSerializer(nil)
	assert.Equal(t, "latency:2|h", serializeOne(t, s, histogram("latency")))
}

func TestUseDistributions(t *testing.T) {
	s := NewDogStatsDSerializer([]string{"env:prod"}, WithDistributions(true))

	assert.Equal(t, "latency:2|d|#env:prod", serializeOne(t, s, histogram("latency")))
	// Only histograms change type.
	assert.Equal(t, "c:1|c|#env:prod", serializeOne(t, s, &models.Metric{Name: "c", Type: models.MetricTypeCounter, Value: 1}))
	assert.Equal(t, "g:3|g|#env:prod", serializeOne(t, s, &models.Metric{Name: "g", Type: models.MetricTypeGauge, Value: 3}))
}

func TestDistributionPrefixes(t *testing.T) {
	s := NewDogStatsDSerializer(nil, WithDistributionPrefixes([]string{"http.", "db.query"}))

	assert.Equal(t, "http.latency:2|d", serializeOne(t, s, histogram("http.latency")))
	assert.Equal(t, "db.query.time:2|d", serializeOne(t, s, histogram("db.query.time")))
	assert.Equal(t, "cache.latency:2|h", serializeOne(t, s, histogram("cache.latency")), "non-matching prefix stays |h")
	assert.Equal(t, "my.http.latency:2|h", serializeOne(t, s, histogram("my.http.latency")), "prefix must match the start of the name")
	// Counters and gauges are unaffected even when the name matches.
	assert.Equal(t, "http.requests:1|c", serializeOne(t, s, &models.Metric{Name: "http.requests", Type: models.MetricTypeCounter, Value: 1}))
	assert.Equal(t, "http.conns:4|g", serializeOne(t, s, &models.Metric{Name: "http.conns", Type: models.MetricTypeGauge, Value: 4}))
}

func TestDistributionPrefixesEmptyPrefixMatchesAll(t *testing.T) {
	s := NewDogStatsDSerializer(nil, WithDistributionPrefixes([]string{""}))
	assert.Equal(t, "x:2|d", serializeOne(t, s, histogram("x")))
}

func TestDistributionPrefixesCopied(t *testing.T) {
	prefixes := []string{"http."}
	s := NewDogStatsDSerializer(nil, WithDistributionPrefixes(prefixes))
	prefixes[0] = "db."

	assert.Equal(t, "http.latency:2|d", serializeOne(t, s, histogram("http.latency")))
	assert.Equal(t, "db.latency:2|h", serializeOne(t, s, histogram("db.latency")))
}

func taggedMetric() *models.Metric {
	return &models.Metric{
		Name: "req", Type: models.MetricTypeCounter, Value: 1,
		Attributes: []attribute.KeyValue{
			attribute.String("http_req_path", "/a/1"),
			attribute.String("method", "GET"),
			attribute.String("status", "200"),
		},
	}
}

func TestTagFiltersStripOnlyListedKeys(t *testing.T) {
	s := NewDogStatsDSerializer(nil, WithTagFilters([]string{"method", "missing"}))
	assert.Equal(t, "req:1|c|#http_req_path:/a/1,status:200", serializeOne(t, s, taggedMetric()))
}

func TestTagFiltersAllStrippedLeavesNoTagSection(t *testing.T) {
	m := &models.Metric{Name: "req", Type: models.MetricTypeCounter, Value: 1,
		Attributes: []attribute.KeyValue{attribute.String("http_req_path", "/x")}}
	s := NewDogStatsDSerializer(nil, WithTagFilters([]string{"http_req_path"}))
	assert.Equal(t, "req:1|c", serializeOne(t, s, m))
}

func TestTagFiltersApplyToGlobalTags(t *testing.T) {
	s := NewDogStatsDSerializer([]string{"env:prod", "http_req_path:/g"}, WithTagFilters([]string{"http_req_path"}))
	assert.Equal(t, "req:1|c|#env:prod,method:GET,status:200", serializeOne(t, s, taggedMetric()))
}

func TestNoTagFiltersKeepAll(t *testing.T) {
	want := "req:1|c|#http_req_path:/a/1,method:GET,status:200"
	assert.Equal(t, want, serializeOne(t, NewDogStatsDSerializer(nil), taggedMetric()))
	assert.Equal(t, want, serializeOne(t, NewDogStatsDSerializer(nil, WithTagFilters([]string{})), taggedMetric()))
}

func TestTagFiltersDoNotMutateMetric(t *testing.T) {
	m := taggedMetric()
	want := append([]attribute.KeyValue(nil), m.Attributes...)
	s := NewDogStatsDSerializer(nil, WithTagFilters([]string{"http_req_path", "method"}))
	serializeOne(t, s, m)
	serializeOne(t, s, m)
	assert.Equal(t, want, m.Attributes)
}

func TestTagFiltersCopied(t *testing.T) {
	keys := []string{"method"}
	s := NewDogStatsDSerializer(nil, WithTagFilters(keys))
	keys[0] = "status"
	assert.Equal(t, "req:1|c|#http_req_path:/a/1,status:200", serializeOne(t, s, taggedMetric()))
}
