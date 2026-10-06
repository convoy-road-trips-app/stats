package runtimemetrics

import (
	"fmt"
	"io"
	"runtime"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats/models"
)

// MetricCollector is something that can sample metrics on demand. It is the
// RecordFunc-based counterpart of segmentio's procstats.Collector: instead of
// reporting to a global engine, Collect emits through the RecordFunc it is
// given, so the same collector can feed the stats pipeline or a test.
//
// The existing runtimemetrics.Collector type is the built-in Go runtime and
// process collector; AsMetricCollector adapts it to this interface.
type MetricCollector interface {
	Collect(record RecordFunc)
}

// CollectorFunc adapts a function to the MetricCollector interface.
type CollectorFunc func(record RecordFunc)

// Collect calls f.
func (f CollectorFunc) Collect(record RecordFunc) { f(record) }

// AsMetricCollector adapts c so it can be composed with MultiCollector. The
// record function passed to Collect is ignored: c keeps emitting through the
// RecordFunc it was created with.
func AsMetricCollector(c *Collector) MetricCollector {
	return CollectorFunc(func(RecordFunc) { c.Collect() })
}

// MultiCollector returns a MetricCollector that runs collectors in order. Nil
// collectors are skipped. A panic in one collector propagates; use
// MultiCollectorWith to isolate collectors from each other.
func MultiCollector(collectors ...MetricCollector) MetricCollector {
	return CollectorFunc(func(record RecordFunc) {
		for _, c := range collectors {
			if c != nil {
				c.Collect(record)
			}
		}
	})
}

// MultiCollectorWith is like MultiCollector but recovers from a panicking
// collector, reports it through onError (which may be nil) and carries on with
// the remaining collectors.
func MultiCollectorWith(onError func(error), collectors ...MetricCollector) MetricCollector {
	return CollectorFunc(func(record RecordFunc) {
		for _, c := range collectors {
			if c != nil {
				safeCollect(c, record, onError)
			}
		}
	})
}

// safeCollect runs c.Collect and converts a panic into an error for onError.
func safeCollect(c MetricCollector, record RecordFunc, onError func(error)) {
	defer func() {
		if r := recover(); r != nil && onError != nil {
			onError(fmt.Errorf("runtimemetrics: collector panic: %v", r))
		}
	}()
	c.Collect(record)
}

// DefaultCollectInterval is the interval StartCollector uses when
// ScheduleConfig.CollectInterval is zero, matching segmentio's procstats.
const DefaultCollectInterval = 15 * time.Second

// ScheduleConfig configures StartCollectorWith.
type ScheduleConfig struct {
	// Collector is run once immediately and then every CollectInterval. A nil
	// Collector collects nothing.
	Collector MetricCollector
	// CollectInterval defaults to DefaultCollectInterval when zero or negative.
	CollectInterval time.Duration
	// OnError, if set, receives a panic recovered from Collector. The schedule
	// keeps running afterwards.
	OnError func(error)
}

// StartCollector runs c every DefaultCollectInterval, emitting through record,
// until the returned io.Closer is closed. A nil record discards metrics.
func StartCollector(c MetricCollector, record RecordFunc) io.Closer {
	return StartCollectorWith(ScheduleConfig{Collector: c}, record)
}

// StartCollectorWith schedules cfg.Collector on its own goroutine. The first
// collection happens right away. Close stops the schedule and waits for any
// in-flight collection to finish; it is safe to call more than once.
func StartCollectorWith(cfg ScheduleConfig, record RecordFunc) io.Closer {
	if cfg.CollectInterval <= 0 {
		cfg.CollectInterval = DefaultCollectInterval
	}
	if cfg.Collector == nil {
		cfg.Collector = MultiCollector()
	}
	if record == nil {
		record = discardRecord
	}

	s := &schedule{stop: make(chan struct{}), join: make(chan struct{})}
	go func() {
		// Collectors often block in syscalls (procfs, sysctl); pinning the
		// goroutine to its thread lets the runtime hand the P to other
		// goroutines sooner, as segmentio's StartCollector does.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(s.join)

		ticker := time.NewTicker(cfg.CollectInterval)
		defer ticker.Stop()

		safeCollect(cfg.Collector, record, cfg.OnError)
		for {
			select {
			case <-ticker.C:
				safeCollect(cfg.Collector, record, cfg.OnError)
			case <-s.stop:
				return
			}
		}
	}()
	return s
}

func discardRecord(string, models.MetricType, float64, ...attribute.KeyValue) {}

type schedule struct {
	once sync.Once
	stop chan struct{}
	join chan struct{}
}

// Close implements io.Closer. It always returns nil.
func (s *schedule) Close() error {
	s.once.Do(func() { close(s.stop) })
	<-s.join
	return nil
}
