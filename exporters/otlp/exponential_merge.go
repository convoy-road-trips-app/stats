package otlp

import (
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// mergeExpo returns a new histogram that combines a and b without changing
// either. The result is what recording every observation of a and b into one
// histogram would give, using maxSize min(a.maxSize, b.maxSize) and starting
// scale min(a.scale, b.scale): the scale is lowered further only as far as the
// combined buckets need, and the buckets, the zero count, the total count, min and max
// match exactly. The sum is the float sum of both sums, so it can differ in
// the last bits from recording order. At scale expoMinScale a range may hold
// maxSize+1 buckets, as it does when recording.
//
// The result shares no memory with a or b.
func mergeExpo(a, b *expoHistogram) *expoHistogram {
	h := newExpoHistogram(min(a.maxSize, b.maxSize), min(a.scale, b.scale))
	for _, src := range []*expoHistogram{a, b} {
		h.replay(&h.positive, &src.positive, src.scale)
		h.replay(&h.negative, &src.negative, src.scale)
		h.zeroCount += src.zeroCount
		h.count += src.count
		h.sum += src.sum
		if src.count > 0 {
			if h.count == src.count || src.min < h.min {
				h.min = src.min
			}
			if h.count == src.count || src.max > h.max {
				h.max = src.max
			}
		}
	}
	return h
}

// replay adds every non-empty bucket of src, whose indexes are at srcScale,
// to dst, which is one of h's ranges. h.scale never exceeds srcScale and may
// drop while replaying, so the shift is recomputed for every bucket.
func (h *expoHistogram) replay(dst, src *expoBuckets, srcScale int32) {
	for i, count := range src.counts {
		if count == 0 {
			continue
		}
		h.addCount(dst, (src.offset+i)>>(srcScale-h.scale), count)
	}
}

// expoHistogramFromPoint rebuilds a histogram from a data point, for example
// one produced by snapshot, so that it can be merged. The buckets are replayed
// at dp.Scale into a histogram of maxSize, so a data point wider than maxSize
// is downscaled. Nothing in dp is aliased.
//
// dp.Scale must lie in [expoMinScale, expoMaxScale] (it is clamped otherwise)
// and dp.Min and dp.Max must be defined when dp.Count > 0.
func expoHistogramFromPoint(dp *metricdata.ExponentialHistogramDataPoint[float64], maxSize int32) *expoHistogram {
	h := newExpoHistogram(maxSize, dp.Scale)
	srcScale := h.scale // dp.Scale, clamped
	// The bucket indexes of dp are at srcScale, which is h.scale until a
	// bucket forces h to downscale, so replay handles both with one shift.
	for _, r := range []struct {
		dst *expoBuckets
		src metricdata.ExponentialBucket
	}{
		{&h.positive, dp.PositiveBucket},
		{&h.negative, dp.NegativeBucket},
	} {
		for i, count := range r.src.Counts {
			h.addCount(r.dst, (int(r.src.Offset)+i)>>(srcScale-h.scale), count)
		}
	}
	h.zeroCount = dp.ZeroCount
	h.count = dp.Count
	h.sum = dp.Sum
	if dp.Count > 0 {
		if v, ok := dp.Min.Value(); ok {
			h.min = v
		}
		if v, ok := dp.Max.Value(); ok {
			h.max = v
		}
	}
	return h
}
