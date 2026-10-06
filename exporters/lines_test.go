package exporters

import (
	"testing"
	"time"

	"github.com/convoy-road-trips-app/stats/models"
	"go.opentelemetry.io/otel/attribute"
)

func TestNewLineSerializer(t *testing.T) {
	s := NewLineSerializer()
	if s.Name() != "dogstatsd" {
		t.Fatalf("Name() = %q, want dogstatsd", s.Name())
	}

	m := &models.Metric{
		Name:       "http.requests",
		Type:       models.MetricTypeCounter,
		Value:      1,
		Timestamp:  time.Now(),
		Attributes: []attribute.KeyValue{attribute.String("method", "GET")},
	}
	got, err := s.Serialize([]*models.Metric{m})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || string(got[0]) != "http.requests:1|c|#method:GET" {
		t.Fatalf("Serialize = %q, want [http.requests:1|c|#method:GET]", got)
	}
}
