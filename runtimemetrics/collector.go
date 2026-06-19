package runtimemetrics

import (
	"context"
	"math"
	"runtime"
	"runtime/metrics"
	"sync"
	"time"

	"github.com/convoy-road-trips-app/stats/models"
)

type RecordFunc func(name string, mtype models.MetricType, value float64)

type Config struct {
	CollectInterval time.Duration
	Prefix          string
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
		{"/gc/heap/goal:bytes", []string{"heap.goal.bytes"}},
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

type Collector struct {
	cfg     Config
	record  RecordFunc
	samples []metrics.Sample
	names   [][]string

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	wg        sync.WaitGroup
}

func New(cfg Config, record RecordFunc) *Collector {
	mappings := getMappings()
	samples := make([]metrics.Sample, len(mappings))
	names := make([][]string, len(mappings))

	prefix := cfg.Prefix
	if prefix != "" && prefix[len(prefix)-1] != '.' {
		prefix += "."
	}

	for i, m := range mappings {
		samples[i].Name = m.runtimeName
		prefixedNames := make([]string, len(m.metricNames))
		for j, n := range m.metricNames {
			prefixedNames[j] = prefix + n
		}
		names[i] = prefixedNames
	}

	return &Collector{
		cfg:     cfg,
		record:  record,
		samples: samples,
		names:   names,
	}
}

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

func (c *Collector) Collect() {
	c.collectOnce()
}

func (c *Collector) collectOnce() {
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

	gomaxprocs := float64(runtime.GOMAXPROCS(0))
	prefix := c.cfg.Prefix
	if prefix != "" && prefix[len(prefix)-1] != '.' {
		prefix += "."
	}
	c.record(prefix+"gomaxprocs", models.MetricTypeGauge, gomaxprocs)
}
