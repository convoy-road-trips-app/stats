package runtimemetrics

import (
	"errors"
	"io"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats/models"
)

func TestCollectorFuncRecords(t *testing.T) {
	rec := &recorder{}
	var c MetricCollector = CollectorFunc(func(record RecordFunc) {
		record("app.queue.depth", models.MetricTypeGauge, 7, attribute.String("type", "main"))
	})
	c.Collect(rec.record)

	v, ok := rec.get("app.queue.depth", "main")
	require.True(t, ok)
	assert.InDelta(t, 7, v, 0)
}

func TestMultiCollectorRunsAllInOrder(t *testing.T) {
	rec := &recorder{}
	mk := func(name string) MetricCollector {
		return CollectorFunc(func(record RecordFunc) { record(name, models.MetricTypeGauge, 1) })
	}
	MultiCollector(mk("a"), nil, mk("b"), MultiCollector(mk("c"))).Collect(rec.record)

	require.Len(t, rec.list, 3)
	assert.Equal(t, []string{"a", "b", "c"}, []string{rec.list[0].name, rec.list[1].name, rec.list[2].name})

	assert.NotPanics(t, func() { MultiCollector().Collect(rec.record) })
}

func TestMultiCollectorIsolatesPanics(t *testing.T) {
	rec := &recorder{}
	var errs []error
	boom := CollectorFunc(func(RecordFunc) { panic("boom") })
	ok := CollectorFunc(func(record RecordFunc) { record("after", models.MetricTypeGauge, 1) })

	mc := MultiCollectorWith(func(err error) { errs = append(errs, err) }, boom, ok)
	assert.NotPanics(t, func() { mc.Collect(rec.record) })

	_, found := rec.get("after", "")
	assert.True(t, found, "collectors after a panicking one still run")
	require.Len(t, errs, 1)
	assert.ErrorContains(t, errs[0], "boom")
}

func TestStartCollectorSchedulesAndCloses(t *testing.T) {
	var n atomic.Int64
	rec := &recorder{}
	c := CollectorFunc(func(record RecordFunc) {
		n.Add(1)
		record("tick", models.MetricTypeGauge, float64(n.Load()))
	})

	closer := StartCollectorWith(ScheduleConfig{Collector: c, CollectInterval: 5 * time.Millisecond}, rec.record)
	require.Eventually(t, func() bool { return n.Load() >= 3 }, 2*time.Second, time.Millisecond)

	require.NoError(t, closer.Close())
	require.NoError(t, closer.Close(), "Close is idempotent")

	after := n.Load()
	time.Sleep(30 * time.Millisecond)
	assert.Equal(t, after, n.Load(), "no collection after Close")
}

func TestStartCollectorCollectsImmediately(t *testing.T) {
	var n atomic.Int64
	closer := StartCollector(CollectorFunc(func(RecordFunc) { n.Add(1) }), nil)
	defer func() { _ = closer.Close() }()

	require.Eventually(t, func() bool { return n.Load() >= 1 }, 2*time.Second, time.Millisecond)
}

func TestStartCollectorPanicReportedAndContinues(t *testing.T) {
	var n atomic.Int64
	errc := make(chan error, 16)
	c := CollectorFunc(func(RecordFunc) {
		if n.Add(1) == 1 {
			panic(errors.New("first fails"))
		}
	})

	closer := StartCollectorWith(ScheduleConfig{
		Collector:       c,
		CollectInterval: 5 * time.Millisecond,
		OnError: func(err error) {
			select {
			case errc <- err:
			default:
			}
		},
	}, nil)
	defer func() { _ = closer.Close() }()

	require.Eventually(t, func() bool { return n.Load() >= 3 }, 2*time.Second, time.Millisecond)
	assert.ErrorContains(t, <-errc, "first fails")
}

func TestStartCollectorNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	closers := make([]io.Closer, 0, 5)
	for range 5 {
		closers = append(closers, StartCollector(CollectorFunc(func(RecordFunc) {}), nil))
	}
	for _, c := range closers {
		require.NoError(t, c.Close())
	}
	// Poll by hand: testify's Eventually runs the condition on its own goroutine.
	deadline := time.Now().Add(time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	assert.LessOrEqual(t, runtime.NumGoroutine(), before)
}

func TestCollectorAdaptsExistingCollector(t *testing.T) {
	rec := &recorder{}
	rt := New(Config{Prefix: "rt"}, rec.record)
	AsMetricCollector(rt).Collect(nil)
	assert.True(t, rec.hasPrefix("rt.goroutines"))
}
