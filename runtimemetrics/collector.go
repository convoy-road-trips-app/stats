package runtimemetrics

import (
	"context"
	"math"
	"runtime"
	"runtime/metrics"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats/models"
)

// RecordFunc is the callback the collector uses to emit metrics.
// The client injects this to route runtime metrics into the pipeline. attrs
// are attached to the emitted metric.
type RecordFunc func(name string, mtype models.MetricType, value float64, attrs ...attribute.KeyValue)

// Config holds the runtime metrics collector configuration.
type Config struct {
	CollectInterval time.Duration
	Prefix          string

	// ProcessMetrics enables process-level metrics (CPU, memory, files, threads).
	ProcessMetrics bool
	// DelayMetrics enables kernel scheduler delay counters (Linux taskstats).
	// The first read failure is reported through OnError("delay", err) once
	// and disables delay collection permanently.
	DelayMetrics bool
	// OnError, if set, is called when a metric source fails. source names the
	// failing source (for example "delay"); the client counts it under
	// ExporterErrors["runtimemetrics.<source>"].
	OnError func(source string, err error)
}

type metricMapping struct {
	runtimeName string
	metricNames []string
}

func getMappings() []metricMapping {
	return []metricMapping{
		{"/memory/classes/heap/objects:bytes", []string{"memory.heap.alloc"}},
		{"/memory/classes/heap/inuse:bytes", []string{"memory.heap.inuse"}},
		{"/memory/classes/heap/idle:bytes", []string{"memory.heap.idle"}},
		{"/memory/classes/heap/released:bytes", []string{"memory.heap.released"}},
		{"/memory/classes/total:bytes", []string{"memory.sys"}},
		{"/memory/classes/heap/stacks:bytes", []string{"memory.stack.inuse"}},
		{"/gc/heap/allocs:bytes", []string{"heap.allocs.bytes"}},
		{"/gc/heap/frees:bytes", []string{"heap.frees.bytes"}},
		{"/gc/heap/allocs:objects", []string{"heap.allocs.objects"}},
		{"/gc/heap/frees:objects", []string{"heap.frees.objects"}},
		{"/gc/heap/objects:objects", []string{"heap.objects.live"}},
		{"/gc/heap/goal:bytes", []string{"heap.goal.bytes", "gc.next.bytes"}},
		{"/gc/cycles/total:gc-cycles", []string{"gc.cycles.total"}},
		{"/cpu/classes/gc/total:cpu-seconds", []string{"gc.cpu.seconds", "cpu.gc.seconds"}},
		{"/sched/goroutines:goroutines", []string{"goroutines"}},
		{"/cgo/go-to-c-calls:calls", []string{"cgo.calls"}},
		{"/cpu/classes/total:cpu-seconds", []string{"cpu.total.seconds"}},
		{"/cpu/classes/user:cpu-seconds", []string{"cpu.user.seconds"}},
		{"/cpu/classes/idle:cpu-seconds", []string{"cpu.idle.seconds"}},
		{"/cpu/classes/scavenge/total:cpu-seconds", []string{"cpu.scavenge.seconds"}},
	}
}

// Collector samples Go runtime metrics and emits them via a RecordFunc.
type Collector struct {
	cfg     Config
	record  RecordFunc
	samples []metrics.Sample
	names   [][]string

	// sampleIdx maps a runtime/metrics name to its index in samples.
	sampleIdx map[string]int
	// derived are the MemStats-parity metrics computed from samples.
	derived []derivedMapping
	// pauseIdx is the index of the GC pause histogram sample, or -1.
	pauseIdx int

	// mu serializes collections; pauses holds the previous histogram snapshot.
	mu     sync.Mutex
	pauses pauseTracker

	// proc is the process metrics state, nil unless Config.ProcessMetrics is
	// set and the platform supports it.
	proc *processState

	// delay is the delay metrics state, nil unless Config.DelayMetrics is set.
	delay *delayState

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	wg        sync.WaitGroup
}

// New creates a Collector. If record is nil, a no-op is used.
func New(cfg Config, record RecordFunc) *Collector {
	if record == nil {
		record = func(string, models.MetricType, float64, ...attribute.KeyValue) {}
	}

	mappings := getMappings()
	derived := getDerivedMappings()
	samples := make([]metrics.Sample, 0, len(mappings)+len(derived)*2+1)
	names := make([][]string, 0, cap(samples))
	sampleIdx := make(map[string]int, cap(samples))

	prefix := normalizePrefix(cfg.Prefix)

	// addSample registers a runtime/metrics name once, with its metric names.
	addSample := func(name string, metricNames []string) {
		if _, ok := sampleIdx[name]; ok {
			return
		}
		sampleIdx[name] = len(samples)
		samples = append(samples, metrics.Sample{Name: name})
		names = append(names, metricNames)
	}

	for _, m := range mappings {
		prefixedNames := make([]string, len(m.metricNames))
		for j, n := range m.metricNames {
			prefixedNames[j] = prefix + n
		}
		addSample(m.runtimeName, prefixedNames)
	}

	// Extra samples feed the derived metrics and the GC pause histogram. They
	// carry no direct metric names.
	for _, d := range derived {
		for _, src := range d.sources {
			addSample(src, nil)
		}
	}
	addSample(gcPausesName, nil)

	var proc *processState
	if cfg.ProcessMetrics {
		proc = newPlatformProcessState()
	}

	var delay *delayState
	if cfg.DelayMetrics {
		delay = newDelayState()
	}

	return &Collector{
		proc:      proc,
		delay:     delay,
		cfg:       cfg,
		record:    record,
		samples:   samples,
		names:     names,
		sampleIdx: sampleIdx,
		derived:   derived,
		pauseIdx:  sampleIdx[gcPausesName],
	}
}

// normalizePrefix returns prefix with exactly one trailing dot, or "" if empty.
func normalizePrefix(prefix string) string {
	if prefix != "" && prefix[len(prefix)-1] != '.' {
		return prefix + "."
	}
	return prefix
}

func (c *Collector) prefix() string { return normalizePrefix(c.cfg.Prefix) }

// Start begins periodic collection. Idempotent.
func (c *Collector) Start() {
	c.startOnce.Do(func() {
		c.stopCh = make(chan struct{})
		c.Collect()

		if c.cfg.CollectInterval <= 0 {
			return
		}

		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			ticker := time.NewTicker(c.cfg.CollectInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					c.Collect()
				case <-c.stopCh:
					return
				}
			}
		}()
	})
}

// Stop halts collection and waits for the goroutine to exit.
func (c *Collector) Stop(ctx context.Context) error {
	c.stopOnce.Do(func() {
		if c.stopCh != nil {
			close(c.stopCh)
		}
	})

	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Collect triggers a single synchronous sample.
func (c *Collector) Collect() {
	c.collectOnce()
}

func (c *Collector) collectOnce() {
	c.mu.Lock()
	defer c.mu.Unlock()

	metrics.Read(c.samples)

	for i := range c.samples {
		s := &c.samples[i]

		var value float64
		switch s.Value.Kind() {
		case metrics.KindUint64:
			value = float64(s.Value.Uint64())
		case metrics.KindFloat64:
			value = s.Value.Float64()
		case metrics.KindFloat64Histogram, metrics.KindBad:
			continue
		default:
			continue
		}

		if math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}

		for _, name := range c.names[i] {
			c.record(name, models.MetricTypeGauge, value)
		}
	}

	c.recordDerived()
	if v := c.samples[c.pauseIdx].Value; v.Kind() == metrics.KindFloat64Histogram {
		c.recordPauseStats(v.Float64Histogram())
	}

	c.collectProcess()
	c.collectDelay()

	gomaxprocs := float64(runtime.GOMAXPROCS(0))
	c.record(c.prefix()+"gomaxprocs", models.MetricTypeGauge, gomaxprocs)
}
