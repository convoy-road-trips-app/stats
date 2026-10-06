package otel

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

type recordedValue struct {
	value float64
	attrs attribute.Set
}

type capturingHistogram struct {
	noop.Float64Histogram
	recorded []recordedValue
}

func (h *capturingHistogram) Record(_ context.Context, value float64, opts ...metric.RecordOption) {
	h.recorded = append(h.recorded, recordedValue{value: value, attrs: metric.NewRecordConfig(opts).Attributes()})
}

func TestClockRecordsStepsAndTotal(t *testing.T) {
	h := &capturingHistogram{}
	start := time.Unix(1000, 0)
	ctx := context.Background()

	clock := NewClockAt(h, start, metric.WithAttributes(attribute.String("job", "import")))
	clock.StampAt(ctx, "fetch", start.Add(1500*time.Millisecond))
	clock.StampAt(ctx, "parse", start.Add(2*time.Second))
	clock.StopAt(ctx, start.Add(3*time.Second))

	want := []struct {
		stamp string
		value float64
	}{{"fetch", 1.5}, {"parse", 0.5}, {"total", 3}}
	if len(h.recorded) != len(want) {
		t.Fatalf("got %d records, want %d", len(h.recorded), len(want))
	}
	for i, w := range want {
		got := h.recorded[i]
		stamp, _ := got.attrs.Value("stamp")
		job, _ := got.attrs.Value("job")
		if got.value != w.value || stamp.AsString() != w.stamp || job.AsString() != "import" || got.attrs.Len() != 2 {
			t.Errorf("record %d = %v %v, want %v stamp=%q job=import", i, got.value, got.attrs.Encoded(attribute.DefaultEncoder()), w.value, w.stamp)
		}
	}
}
