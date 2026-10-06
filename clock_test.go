package stats

import (
	"context"
	"testing"
	"time"
)

type clockObservation struct {
	name  string
	value float64
	attrs map[string]string
}

// capturingRecorder embeds the Recorder interface, not NoOpClient, so it
// implements Histogram only and not DurationObserver.
type capturingRecorder struct {
	Recorder
	observed []clockObservation
}

func (r *capturingRecorder) Histogram(_ context.Context, name string, value float64, opts ...MetricOption) error {
	m := &Metric{}
	for _, opt := range opts {
		opt(m)
	}
	attrs := make(map[string]string, len(m.Attributes))
	for _, kv := range m.Attributes {
		attrs[string(kv.Key)] = kv.Value.AsString()
	}
	r.observed = append(r.observed, clockObservation{name: name, value: value, attrs: attrs})
	return nil
}

func TestClockStampsStepsAndTotal(t *testing.T) {
	rec := &capturingRecorder{}
	start := time.Unix(1000, 0)
	ctx := context.Background()

	clock := NewClockAt(rec, "job.duration", start, WithAttribute("job", "import"))
	if err := clock.StampAt(ctx, "fetch", start.Add(1500*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if err := clock.StampAt(ctx, "parse", start.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := clock.StopAt(ctx, start.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}

	want := []struct {
		stamp string
		value float64
	}{{"fetch", 1.5}, {"parse", 0.5}, {StampTotal, 3}}
	if len(rec.observed) != len(want) {
		t.Fatalf("got %d observations, want %d", len(rec.observed), len(want))
	}
	for i, w := range want {
		got := rec.observed[i]
		if got.name != "job.duration" || got.value != w.value || got.attrs[StampTag] != w.stamp || got.attrs["job"] != "import" {
			t.Errorf("observation %d = %+v, want stamp %q value %v job import", i, got, w.stamp, w.value)
		}
		if len(got.attrs) != 2 {
			t.Errorf("observation %d has attributes %v, want job and stamp only", i, got.attrs)
		}
	}
}

func TestClientClockRecordsHistogram(t *testing.T) {
	client, err := NewClient(WithServiceName("clock-test"))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	clock := client.Clock("job.duration")
	if err := clock.Stamp(context.Background(), "step"); err != nil {
		t.Fatal(err)
	}
	if err := clock.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// observingRecorder also implements DurationObserver.
type observingRecorder struct {
	capturingRecorder
	observeCalls []time.Duration
	observeName  string
	observeAttrs map[string]string
}

func (r *observingRecorder) Observe(_ context.Context, name string, d time.Duration, opts ...MetricOption) error {
	m := &Metric{}
	for _, opt := range opts {
		opt(m)
	}
	r.observeName = name
	r.observeAttrs = make(map[string]string, len(m.Attributes))
	for _, kv := range m.Attributes {
		r.observeAttrs[string(kv.Key)] = kv.Value.AsString()
	}
	r.observeCalls = append(r.observeCalls, d)
	return nil
}

func TestClockUsesObserveWhenRecorderImplementsDurationObserver(t *testing.T) {
	rec := &observingRecorder{}
	start := time.Unix(1000, 0)

	clock := NewClockAt(rec, "job.duration", start, WithAttribute("job", "import"))
	if err := clock.StampAt(context.Background(), "fetch", start.Add(1500*time.Millisecond)); err != nil {
		t.Fatal(err)
	}

	if len(rec.observeCalls) != 1 || rec.observeCalls[0] != 1500*time.Millisecond {
		t.Fatalf("Observe calls = %v, want one call of 1.5s", rec.observeCalls)
	}
	if rec.observeName != "job.duration" || rec.observeAttrs[StampTag] != "fetch" || rec.observeAttrs["job"] != "import" {
		t.Errorf("Observe got name %q attrs %v, want job.duration with stamp fetch and job import", rec.observeName, rec.observeAttrs)
	}
	if len(rec.observed) != 0 {
		t.Errorf("Histogram called %d times, want 0 when Observe is available", len(rec.observed))
	}
}

func TestClockFallsBackToHistogramSeconds(t *testing.T) {
	rec := &capturingRecorder{}
	start := time.Unix(1000, 0)

	clock := NewClockAt(rec, "job.duration", start)
	if err := clock.StopAt(context.Background(), start.Add(2500*time.Millisecond)); err != nil {
		t.Fatal(err)
	}

	if len(rec.observed) != 1 || rec.observed[0].value != 2.5 || rec.observed[0].attrs[StampTag] != StampTotal {
		t.Fatalf("observations = %+v, want one histogram of 2.5 seconds with stamp total", rec.observed)
	}
}
