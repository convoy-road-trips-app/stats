package serializers

import (
	"bytes"
	"fmt"
	"sync"

	"github.com/convoy-road-trips-app/stats/models"
)

// DogStatsDSerializer serializes metrics to Datadog DogStatsD format
// Format: metric.name:value|type|@sample_rate|#tag1:value1,tag2:value2
type DogStatsDSerializer struct {
	bufferPool *sync.Pool
	globalTags []string
}

// NewDogStatsDSerializer creates a new DogStatsD serializer
func NewDogStatsDSerializer(globalTags []string) *DogStatsDSerializer {
	return &DogStatsDSerializer{
		bufferPool: &sync.Pool{
			New: func() any {
				return bytes.NewBuffer(make([]byte, 0, 512))
			},
		},
		globalTags: globalTags,
	}
}

// Name returns the serializer name
func (s *DogStatsDSerializer) Name() string {
	return "dogstatsd"
}

// Serialize converts metrics to DogStatsD format
func (s *DogStatsDSerializer) Serialize(metrics []*models.Metric) ([][]byte, error) {
	packets := make([][]byte, 0, len(metrics))

	for _, metric := range metrics {
		buf := s.bufferPool.Get().(*bytes.Buffer)
		buf.Reset()

		// Format: metric.name:value|type
		fmt.Fprintf(buf, "%s:%g|%s", metric.Name, metric.Value, s.metricType(metric.Type))

		// Add tags if present
		if len(metric.Attributes) > 0 || len(s.globalTags) > 0 {
			buf.WriteString("|#")

			// Global tags first
			for i, tag := range s.globalTags {
				if i > 0 {
					buf.WriteByte(',')
				}
				buf.WriteString(tag)
			}

			// Metric-specific tags
			for i, attr := range metric.Attributes {
				if i > 0 || len(s.globalTags) > 0 {
					buf.WriteByte(',')
				}
				fmt.Fprintf(buf, "%s:%s", attr.Key, attr.Value.Emit())
			}
		}

		// Make a copy since we're returning the buffer to the pool
		packet := make([]byte, buf.Len())
		copy(packet, buf.Bytes())
		packets = append(packets, packet)

		s.bufferPool.Put(buf)
	}

	return packets, nil
}

// metricType converts internal metric type to DogStatsD type
func (s *DogStatsDSerializer) metricType(t models.MetricType) string {
	switch t {
	case models.MetricTypeCounter:
		return "c"
	case models.MetricTypeGauge:
		return "g"
	case models.MetricTypeHistogram:
		return "h"
	default:
		return "c"
	}
}
