package serializers

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/convoy-road-trips-app/stats/models"
)

// DogStatsDSerializer serializes metrics to Datadog DogStatsD format
// Format: metric.name:value|type|@sample_rate|#tag1:value1,tag2:value2
type DogStatsDSerializer struct {
	bufferPool *sync.Pool
	globalTags []string

	distributions        bool
	distributionPrefixes []string
}

// DogStatsDOption configures a DogStatsDSerializer.
type DogStatsDOption func(*DogStatsDSerializer)

// WithDistributions makes the serializer emit every histogram as a Datadog
// distribution ("|d") instead of a histogram ("|h").
func WithDistributions(enabled bool) DogStatsDOption {
	return func(s *DogStatsDSerializer) { s.distributions = enabled }
}

// WithDistributionPrefixes makes the serializer emit histograms whose full
// metric name starts with one of prefixes as distributions ("|d"). The slice
// is copied. An empty prefix matches every name.
func WithDistributionPrefixes(prefixes []string) DogStatsDOption {
	return func(s *DogStatsDSerializer) { s.distributionPrefixes = slices.Clone(prefixes) }
}

// NewDogStatsDSerializer creates a new DogStatsD serializer
func NewDogStatsDSerializer(globalTags []string, opts ...DogStatsDOption) *DogStatsDSerializer {
	s := &DogStatsDSerializer{
		bufferPool: &sync.Pool{
			New: func() any {
				return bytes.NewBuffer(make([]byte, 0, 512))
			},
		},
		globalTags: globalTags,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
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
		fmt.Fprintf(buf, "%s:%g|%s", metric.Name, metric.Value, s.metricType(metric))

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

// metricType converts the internal metric type to its DogStatsD type.
// Histograms become distributions ("d") when distributions are enabled or the
// full metric name starts with a configured distribution prefix.
func (s *DogStatsDSerializer) metricType(m *models.Metric) string {
	switch m.Type {
	case models.MetricTypeCounter:
		return "c"
	case models.MetricTypeGauge:
		return "g"
	case models.MetricTypeHistogram:
		if s.isDistribution(m.Name) {
			return "d"
		}
		return "h"
	default:
		return "c"
	}
}

// isDistribution reports whether a histogram named name is sent as a distribution.
func (s *DogStatsDSerializer) isDistribution(name string) bool {
	if s.distributions {
		return true
	}
	for _, prefix := range s.distributionPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
