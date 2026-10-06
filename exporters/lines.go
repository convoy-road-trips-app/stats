package exporters

import "github.com/convoy-road-trips-app/stats/serializers"

// NewLineSerializer returns a Serializer that renders one human-readable
// StatsD-format line per metric, such as `http.requests:1|c|#method:GET`.
// It uses the DogStatsD wire format, because the plain StatsD one folds
// attributes into the metric name instead of emitting tags. The returned
// serializer holds no per-call state and is safe for concurrent use.
func NewLineSerializer() Serializer {
	return serializers.NewDogStatsDSerializer(nil)
}
