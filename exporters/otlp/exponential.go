package otlp

import (
	"math"
	"slices"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// Limits of the exponential histogram parameters, the same as the OTel SDK's.
// At scale -10 a bucket spans a factor of 2^1024, so every finite float64
// fits in three buckets; scale 20 is the finest resolution the SDK allows.
const (
	expoMinScale int32 = -10
	expoMaxScale int32 = 20
	expoMinSize  int32 = 2
)

// expoHistogram aggregates observations into an OTLP exponential histogram.
// The OTel SDK aggregator is internal, so this follows the specification:
// https://opentelemetry.io/docs/specs/otel/metrics/data-model/#exponentialhistogram
//
// It starts at maxScale. When an observation would need more than maxSize
// buckets in its range, the scale of both ranges is lowered just enough for
// it to fit. Because downscaling and expoIndex agree exactly, the result
// depends only on the observed values, not on their order.
type expoHistogram struct {
	maxSize int32 // most buckets a range holds above expoMinScale
	scale   int32 // only ever lowered

	count     uint64
	zeroCount uint64 // observations of 0 and -0
	sum       float64
	min       float64
	max       float64

	positive expoBuckets // buckets of v > 0
	negative expoBuckets // buckets of |v| for v < 0
}

// expoBuckets is a contiguous range of buckets: counts[i] is the count of the
// bucket with index offset+i. Recording only grows the range to the bucket it
// counts into, so the first and last counts are never zero.
type expoBuckets struct {
	offset int
	counts []uint64
}

// newExpoHistogram returns an empty histogram at scale maxScale that keeps at
// most maxSize buckets per range. maxScale is clamped to
// [expoMinScale, expoMaxScale] and maxSize is raised to at least expoMinSize.
func newExpoHistogram(maxSize, maxScale int32) *expoHistogram {
	return &expoHistogram{
		maxSize: max(maxSize, expoMinSize),
		scale:   min(max(maxScale, expoMinScale), expoMaxScale),
	}
}

// expoIndex returns the index at scale of the bucket holding v, a positive
// finite value.
//
// At scale s the bucket boundaries are the powers of base = 2^(2^-s), and
// bucket i holds base^i < v <= base^(i+1), so
//
//	index = ceil(log2(v) * 2^s) - 1
//
// An exact power of the base closes the bucket below it. math.Frexp splits
// v = frac * 2^exp with frac in [0.5, 1), so v is a power of two exactly when
// frac == 0.5, and then v = 2^(exp-1).
//
//   - s <= 0: a bucket spans 2^-s whole powers of two, so only the binary
//     exponent matters and the index is exact. At scale 0 a value in
//     (2^(exp-1), 2^exp) has index exp-1 and the power of two 2^(exp-1) has
//     index exp-2. A coarser scale divides by 2^-s, rounding toward minus
//     infinity: index0 >> -s.
//   - s > 0: log2(v) = exp + log2(frac), so
//     index = exp<<s + ceil(log2(frac) * 2^s) - 1. Only the fraction goes
//     through math.Log2, and a power of two takes the exact index
//     (exp-1)<<s - 1. The result is clamped to the buckets between 2^(exp-1)
//     and 2^exp, so rounding in math.Log2 cannot move v across a power of two.
//
// Multiplying by 2^s is exact, so for every scale s,
// expoIndex(v, s) >> k == expoIndex(v, s-k): downscaling the buckets moves
// each value to the bucket it would have been mapped to at the lower scale.
//
// Indexes lie in [-1074<<20 - 1, 1024<<20 - 1], within int32 on every platform.
func expoIndex(v float64, scale int32) int {
	frac, exp := math.Frexp(v)
	powerOfTwo := frac == 0.5
	if scale <= 0 {
		index := exp - 1
		if powerOfTwo {
			index--
		}
		return index >> -scale
	}
	if powerOfTwo {
		return (exp-1)<<scale - 1
	}
	index := exp<<scale + int(math.Ceil(math.Ldexp(math.Log2(frac), int(scale)))) - 1
	return min(max(index, (exp-1)<<scale), exp<<scale-1)
}

// record adds one observation. NaN and ±Inf have no bucket and are ignored;
// 0 and -0 are counted as zero.
func (h *expoHistogram) record(v float64) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return
	}
	if h.count == 0 || v < h.min {
		h.min = v
	}
	if h.count == 0 || v > h.max {
		h.max = v
	}
	h.count++
	h.sum += v

	switch {
	case v > 0:
		h.recordBucket(&h.positive, v)
	case v < 0:
		h.recordBucket(&h.negative, -v)
	default:
		h.zeroCount++
	}
}

// recordBucket counts magnitude m into b, first lowering the scale of both
// ranges if b would otherwise exceed maxSize buckets.
func (h *expoHistogram) recordBucket(b *expoBuckets, m float64) {
	index := expoIndex(m, h.scale)
	if change := h.scaleChange(b, index); change > 0 {
		h.scale -= change
		h.positive.downscale(change)
		h.negative.downscale(change)
		index >>= change
	}
	b.increment(index)
}

// scaleChange returns by how much the scale must drop for b to hold index in
// at most maxSize buckets. It stops at expoMinScale, where a range can hold
// three buckets: values up to 2^-1024, up to 1, and above 1. With maxSize 2
// that range keeps its third bucket rather than losing an observation.
func (h *expoHistogram) scaleChange(b *expoBuckets, index int) int32 {
	if len(b.counts) == 0 {
		return 0
	}
	// At scale 20 two indexes can be more than 2^31 apart, which overflows
	// a 32-bit int, so the width is compared in int64.
	low := int64(min(b.offset, index))
	high := int64(max(b.offset+len(b.counts)-1, index))
	var change int32
	for high-low >= int64(h.maxSize) && h.scale-change > expoMinScale {
		low >>= 1
		high >>= 1
		change++
	}
	return change
}

// downscale lowers the scale of b by change, adding up each run of 2^change
// adjacent buckets into one.
func (b *expoBuckets) downscale(change int32) {
	if len(b.counts) == 0 {
		return
	}
	offset := b.offset >> change
	last := (b.offset + len(b.counts) - 1) >> change
	counts := make([]uint64, last-offset+1)
	for i, count := range b.counts {
		counts[(b.offset+i)>>change-offset] += count
	}
	b.offset = offset
	b.counts = counts
}

// increment adds one to the bucket index, growing the range to include it.
func (b *expoBuckets) increment(index int) {
	switch {
	case len(b.counts) == 0:
		b.offset = index
		b.counts = []uint64{0}
	case index < b.offset:
		counts := make([]uint64, b.offset+len(b.counts)-index)
		copy(counts[b.offset-index:], b.counts)
		b.offset = index
		b.counts = counts
	case index >= b.offset+len(b.counts):
		b.counts = append(b.counts, make([]uint64, index-b.offset-len(b.counts)+1)...)
	}
	b.counts[index-b.offset]++
}

// snapshot returns the histogram as a data point with copied bucket counts,
// so later observations do not change it. Attributes, timestamps and
// exemplars are left to the caller.
func (h *expoHistogram) snapshot() metricdata.ExponentialHistogramDataPoint[float64] {
	dp := metricdata.ExponentialHistogramDataPoint[float64]{
		Count:          h.count,
		Sum:            h.sum,
		Scale:          h.scale,
		ZeroCount:      h.zeroCount,
		PositiveBucket: h.positive.snapshot(),
		NegativeBucket: h.negative.snapshot(),
	}
	if h.count > 0 {
		dp.Min = metricdata.NewExtrema(h.min)
		dp.Max = metricdata.NewExtrema(h.max)
	}
	return dp
}

func (b *expoBuckets) snapshot() metricdata.ExponentialBucket {
	return metricdata.ExponentialBucket{
		Offset: bucketOffset(b.offset),
		Counts: slices.Clone(b.counts),
	}
}

// bucketOffset converts a bucket index to int32. expoIndex keeps every index
// within int32, so the guard never applies.
func bucketOffset(index int) int32 {
	if index < math.MinInt32 || index > math.MaxInt32 {
		return 0
	}
	return int32(index)
}
