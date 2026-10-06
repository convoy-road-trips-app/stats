package stats_test

import (
	"time"

	"github.com/convoy-road-trips-app/stats/models"
	"github.com/convoy-road-trips-app/stats/statstest"
)

// funcMetrics is the example from the segmentio/stats README.
type funcMetrics struct {
	calls struct {
		count int           `metric:"count" type:"counter"`
		time  time.Duration `metric:"time" type:"histogram"`
	} `metric:"func.calls"`
}

// namedMetrics returns every captured metric called name.
func namedMetrics(exp *statstest.Exporter, name string) []models.Metric {
	var out []models.Metric
	all := exp.Metrics()
	for i := range all {
		if all[i].Name == name {
			out = append(out, all[i])
		}
	}
	return out
}

func attrValue(m *models.Metric, key string) (string, bool) {
	for _, kv := range m.Attributes {
		if string(kv.Key) == key {
			return kv.Value.AsString(), true
		}
	}
	return "", false
}

type reportNode struct {
	V    int         `metric:"v"`
	Next *reportNode `metric:"next"`
}

func nanValue() float64 {
	zero := 0.0
	return zero / zero
}
