package runtimemetrics

import (
	"math"
	"runtime/metrics"

	"github.com/convoy-road-trips-app/stats/models"
)

// gcPausesName is the cumulative GC pause histogram in runtime/metrics. It is
// available on every supported Go version; collection skips it when absent.
const gcPausesName = "/gc/pauses:seconds"

// derivedMapping describes a MemStats-style metric computed from one or more
// runtime/metrics samples. The metric is skipped when any source is missing on
// the running Go version or when combine reports ok=false.
type derivedMapping struct {
	metricName string
	sources    []string
	combine    func(vals []float64) (float64, bool)
}

func sumValues(vals []float64) (float64, bool) {
	var total float64
	for _, v := range vals {
		total += v
	}
	return total, true
}

// fractionOf returns vals[0]/vals[1]. A zero denominator (the runtime has not
// yet accounted any CPU time) yields 0, matching MemStats.GCCPUFraction before
// the first GC, rather than NaN.
func fractionOf(vals []float64) (float64, bool) {
	if vals[1] <= 0 {
		return 0, true
	}
	return vals[0] / vals[1], true
}

// getDerivedMappings lists the MemStats-parity metrics that are derived from
// runtime/metrics samples (never from runtime.ReadMemStats, which stops the
// world).
func getDerivedMappings() []derivedMapping {
	return []derivedMapping{
		{"memory.stack.sys", []string{"/memory/classes/heap/stacks:bytes", "/memory/classes/os-stacks:bytes"}, sumValues},
		{"memory.mspan.inuse", []string{"/memory/classes/metadata/mspan/inuse:bytes"}, sumValues},
		{"memory.mspan.sys", []string{"/memory/classes/metadata/mspan/inuse:bytes", "/memory/classes/metadata/mspan/free:bytes"}, sumValues},
		{"memory.mcache.inuse", []string{"/memory/classes/metadata/mcache/inuse:bytes"}, sumValues},
		{"memory.mcache.sys", []string{"/memory/classes/metadata/mcache/inuse:bytes", "/memory/classes/metadata/mcache/free:bytes"}, sumValues},
		{"memory.buckhash.sys", []string{"/memory/classes/profiling/buckets:bytes"}, sumValues},
		{"memory.gc.sys", []string{"/memory/classes/metadata/other:bytes"}, sumValues},
		{"memory.other.sys", []string{"/memory/classes/other:bytes"}, sumValues},
		// MemStats.HeapInuse = spans in use (objects + unused); HeapIdle = free + released.
		{"memory.heap.inuse", []string{"/memory/classes/heap/objects:bytes", "/memory/classes/heap/unused:bytes"}, sumValues},
		{"memory.heap.idle", []string{"/memory/classes/heap/free:bytes", "/memory/classes/heap/released:bytes"}, sumValues},
		{"memory.alloc", []string{"/memory/classes/heap/objects:bytes"}, sumValues},
		{"memory.heap.sys", []string{"/memory/classes/heap/objects:bytes", "/memory/classes/heap/unused:bytes", "/memory/classes/heap/free:bytes", "/memory/classes/heap/released:bytes"}, sumValues},
		{"gc.cpu.fraction", []string{"/cpu/classes/gc/total:cpu-seconds", "/cpu/classes/total:cpu-seconds"}, fractionOf},
	}
}

// pauseTracker diffs successive snapshots of the cumulative GC pause histogram.
type pauseTracker struct {
	prevCounts  []uint64
	prevBuckets []float64
}

// delta returns the per-bucket counts added since the previous snapshot and
// the bucket boundaries they apply to. It stores a deep copy of h.Counts as
// the new baseline because the runtime may reuse the slice backing a sample.
// ok is false when no baseline exists yet for a changed bucket layout, or the
// histogram is malformed.
func (p *pauseTracker) delta(h *metrics.Float64Histogram) (counts []uint64, buckets []float64, ok bool) {
	if h == nil || len(h.Buckets) != len(h.Counts)+1 {
		return nil, nil, false
	}

	cur := make([]uint64, len(h.Counts))
	copy(cur, h.Counts)
	bounds := make([]float64, len(h.Buckets))
	copy(bounds, h.Buckets)

	prev := p.prevCounts
	layoutChanged := p.prevCounts != nil && len(prev) != len(cur)
	p.prevCounts = cur
	p.prevBuckets = bounds
	if layoutChanged {
		return nil, nil, false
	}

	counts = make([]uint64, len(cur))
	for i, c := range cur {
		var before uint64
		if prev != nil {
			before = prev[i]
		}
		if c < before {
			// Counter went backwards; treat the snapshot as a reset.
			return nil, nil, false
		}
		counts[i] = c - before
	}
	return counts, bounds, true
}

// pauseStats summarizes histogram delta counts. min and max are the lower
// bound of the lowest and the upper bound of the highest non-empty bucket, and
// avg weights each bucket's midpoint by its count, so all three are bucket
// approximations rather than exact pause durations. ok is false when the delta
// holds no pauses.
func pauseStats(counts []uint64, buckets []float64) (minV, maxV, avg float64, ok bool) {
	var total uint64
	var weighted float64
	first, last := -1, -1
	for i, c := range counts {
		if c == 0 {
			continue
		}
		if first < 0 {
			first = i
		}
		last = i
		total += c
		weighted += float64(c) * bucketMid(buckets[i], buckets[i+1])
	}
	if total == 0 {
		return 0, 0, 0, false
	}

	minV = buckets[first]
	if math.IsInf(minV, 0) {
		minV = buckets[first+1]
	}
	maxV = buckets[last+1]
	if math.IsInf(maxV, 0) {
		maxV = buckets[last]
	}
	avg = weighted / float64(total)
	if !isFinite(minV) || !isFinite(maxV) || !isFinite(avg) {
		return 0, 0, 0, false
	}
	return minV, maxV, avg, true
}

// bucketMid returns the midpoint of [lo, hi), falling back to the finite bound
// when the bucket is open ended.
func bucketMid(lo, hi float64) float64 {
	switch {
	case math.IsInf(lo, 0):
		return hi
	case math.IsInf(hi, 0):
		return lo
	default:
		return (lo + hi) / 2
	}
}

func isFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// recordPauseStats emits gc.pause.seconds.{min,max,avg} from the GC pause
// histogram delta since the previous call. Nothing is emitted when there were
// no pauses in the interval. It is the unexported seam used by tests to inject
// fake histogram snapshots.
func (c *Collector) recordPauseStats(h *metrics.Float64Histogram) {
	counts, buckets, ok := c.pauses.delta(h)
	if !ok {
		return
	}
	minV, maxV, avg, ok := pauseStats(counts, buckets)
	if !ok {
		return
	}
	prefix := c.prefix()
	c.record(prefix+"gc.pause.seconds.min", models.MetricTypeGauge, minV)
	c.record(prefix+"gc.pause.seconds.max", models.MetricTypeGauge, maxV)
	c.record(prefix+"gc.pause.seconds.avg", models.MetricTypeGauge, avg)
}

// recordDerived emits the derived MemStats-parity gauges whose source samples
// are all valid scalars on the running Go version.
func (c *Collector) recordDerived() {
	prefix := c.prefix()
	for _, d := range c.derived {
		vals := make([]float64, len(d.sources))
		valid := true
		for i, src := range d.sources {
			v, ok := c.scalar(src)
			if !ok {
				valid = false
				break
			}
			vals[i] = v
		}
		if !valid {
			continue
		}
		v, ok := d.combine(vals)
		if !ok || !isFinite(v) {
			continue
		}
		c.record(prefix+d.metricName, models.MetricTypeGauge, v)
	}
}

// scalar returns the current value of the named sample when it is a finite
// scalar, and false when the metric is unknown to this Go version.
func (c *Collector) scalar(name string) (float64, bool) {
	idx, found := c.sampleIdx[name]
	if !found {
		return 0, false
	}
	var v float64
	switch s := c.samples[idx].Value; s.Kind() {
	case metrics.KindUint64:
		v = float64(s.Uint64())
	case metrics.KindFloat64:
		v = s.Float64()
	case metrics.KindFloat64Histogram, metrics.KindBad:
		return 0, false
	default:
		return 0, false
	}
	return v, isFinite(v)
}
