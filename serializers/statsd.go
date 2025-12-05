package serializers

import (
	"bytes"
	"fmt"
	"sync"

	"github.com/convoy-road-trips-app/stats/models"
)

// StatsDSerializer serializes metrics to StatsD format for Prometheus
// Format: metric.name:value|type
type StatsDSerializer struct {
	bufferPool *sync.Pool
	prefix     string
}

// NewStatsDSerializer creates a new StatsD serializer
func NewStatsDSerializer(prefix string) *StatsDSerializer {
	return &StatsDSerializer{
		bufferPool: &sync.Pool{
			New: func() any {
				return bytes.NewBuffer(make([]byte, 0, 512))
			},
		},
		prefix: prefix,
	}
}

// Name returns the serializer name
func (s *StatsDSerializer) Name() string {
	return "statsd"
}

// Serialize converts metrics to StatsD format
func (s *StatsDSerializer) Serialize(metrics []*models.Metric) ([][]byte, error) {
	packets := make([][]byte, 0, len(metrics))

	for _, metric := range metrics {
		buf := s.bufferPool.Get().(*bytes.Buffer)
		buf.Reset()

		// Build metric name with attributes as labels
		metricName := s.buildMetricName(metric)

		// Format: metric.name:value|type
		fmt.Fprintf(buf, "%s:%g|%s", metricName, metric.Value, s.metricType(metric.Type))

		// Make a copy
		packet := make([]byte, buf.Len())
		copy(packet, buf.Bytes())
		packets = append(packets, packet)

		s.bufferPool.Put(buf)
	}

	return packets, nil
}

// buildMetricName constructs metric name with attributes
// For StatsD, we encode attributes in the metric name since standard StatsD doesn't support tags
// Format: prefix.metric_name.attr1_value1.attr2_value2
func (s *StatsDSerializer) buildMetricName(metric *models.Metric) string {
	buf := s.bufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer s.bufferPool.Put(buf)

	// Add prefix if set
	if s.prefix != "" {
		buf.WriteString(s.prefix)
		buf.WriteByte('.')
	}

	// Add metric name
	buf.WriteString(metric.Name)

	// Add attributes as part of metric name (StatsD doesn't support tags natively)
	for _, attr := range metric.Attributes {
		buf.WriteByte('.')
		fmt.Fprintf(buf, "%s_%s", attr.Key, attr.Value.Emit())
	}

	return buf.String()
}

// metricType converts internal metric type to StatsD type
func (s *StatsDSerializer) metricType(t models.MetricType) string {
	switch t {
	case models.MetricTypeCounter:
		return "c"
	case models.MetricTypeGauge:
		return "g"
	case models.MetricTypeHistogram:
		return "ms" // StatsD uses 'ms' for timing/histogram
	default:
		return "c"
	}
}
