package otlp

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func recordAll(h *expoHistogram, values ...float64) {
	for _, v := range values {
		h.record(v)
	}
}

func TestExpoScale0Values1_2_4(t *testing.T) {
	// Given
	h := newExpoHistogram(160, 0)

	// When
	recordAll(h, 1, 2, 4)

	// Then
	assert.Equal(t, metricdata.ExponentialHistogramDataPoint[float64]{
		Count:          3,
		Sum:            7,
		Min:            metricdata.NewExtrema(1.0),
		Max:            metricdata.NewExtrema(4.0),
		Scale:          0,
		PositiveBucket: metricdata.ExponentialBucket{Offset: -1, Counts: []uint64{1, 1, 1}},
	}, h.snapshot())
}

func TestExpoZeroCount(t *testing.T) {
	// Given
	h := newExpoHistogram(160, 0)

	// When
	recordAll(h, 0, math.Copysign(0, -1), 0, 5)

	// Then
	assert.Equal(t, metricdata.ExponentialHistogramDataPoint[float64]{
		Count:          4,
		Sum:            5,
		Min:            metricdata.NewExtrema(0.0),
		Max:            metricdata.NewExtrema(5.0),
		Scale:          0,
		ZeroCount:      3,
		PositiveBucket: metricdata.ExponentialBucket{Offset: 2, Counts: []uint64{1}},
	}, h.snapshot())
}

func TestExpoNegative(t *testing.T) {
	// Given
	h := newExpoHistogram(160, 0)

	// When
	recordAll(h, -1, -2, -4, 3)

	// Then
	assert.Equal(t, metricdata.ExponentialHistogramDataPoint[float64]{
		Count:          4,
		Sum:            -4,
		Min:            metricdata.NewExtrema(-4.0),
		Max:            metricdata.NewExtrema(3.0),
		Scale:          0,
		PositiveBucket: metricdata.ExponentialBucket{Offset: 1, Counts: []uint64{1}},
		NegativeBucket: metricdata.ExponentialBucket{Offset: -1, Counts: []uint64{1, 1, 1}},
	}, h.snapshot())
}

func TestExpoDownscaleOnOverflow(t *testing.T) {
	t.Run("lowest scale that fits", func(t *testing.T) {
		// Given
		h := newExpoHistogram(3, 20)

		// When / Then
		h.record(1)
		assert.Equal(t, int32(20), h.snapshot().Scale)

		// 2 is 2^20 buckets above 1 at scale 20; at scale 1 the indexes are -1 and 1.
		h.record(2)
		dp := h.snapshot()
		assert.Equal(t, int32(1), dp.Scale)
		assert.Equal(t, metricdata.ExponentialBucket{Offset: -1, Counts: []uint64{1, 0, 1}}, dp.PositiveBucket)

		h.record(4)
		dp = h.snapshot()
		assert.Equal(t, int32(0), dp.Scale)
		assert.Equal(t, metricdata.ExponentialBucket{Offset: -1, Counts: []uint64{1, 1, 1}}, dp.PositiveBucket)
	})

	t.Run("scale is shared by both ranges", func(t *testing.T) {
		// Given
		h := newExpoHistogram(2, 20)

		// When
		recordAll(h, -3, 1, 4)

		// Then: 1 and 4 only fit two buckets at scale -1, where 3 is in (1, 4].
		dp := h.snapshot()
		assert.Equal(t, int32(-1), dp.Scale)
		assert.Equal(t, metricdata.ExponentialBucket{Offset: -1, Counts: []uint64{1, 1}}, dp.PositiveBucket)
		assert.Equal(t, metricdata.ExponentialBucket{Offset: 0, Counts: []uint64{1}}, dp.NegativeBucket)
	})

	t.Run("ranges never exceed maxSize", func(t *testing.T) {
		// Given
		h := newExpoHistogram(20, 20)

		// When
		for i := range 2000 {
			v := math.Pow(10, float64(i%11)-5) * (1 + float64(i)/2000)
			if i%3 == 0 {
				v = -v
			}
			h.record(v)
		}

		// Then
		dp := h.snapshot()
		assert.LessOrEqual(t, len(dp.PositiveBucket.Counts), 20)
		assert.LessOrEqual(t, len(dp.NegativeBucket.Counts), 20)
		assert.Equal(t, dp.Count, dp.ZeroCount+sum(dp.PositiveBucket.Counts)+sum(dp.NegativeBucket.Counts))
		assert.NotZero(t, dp.PositiveBucket.Counts[0])
		assert.NotZero(t, dp.PositiveBucket.Counts[len(dp.PositiveBucket.Counts)-1])
	})
}

func sum(counts []uint64) uint64 {
	var total uint64
	for _, c := range counts {
		total += c
	}
	return total
}

func TestExpoExtremeValues(t *testing.T) {
	// At scale 0, 1e-300 is in bucket -997 = (2^-997, 2^-996] and 1e300 in
	// bucket 996 = (2^996, 2^997]. Scale -4 is the first where they are at
	// most 160 buckets apart: -997>>4 = -63 and 996>>4 = 62.
	wide := make([]uint64, 126)
	wide[0], wide[125] = 1, 1

	tests := []struct {
		name    string
		maxSize int32
		values  []float64
		want    metricdata.ExponentialHistogramDataPoint[float64]
	}{
		{
			name:    "default size",
			maxSize: 160,
			values:  []float64{1e-300, 1e300},
			want: metricdata.ExponentialHistogramDataPoint[float64]{
				Count:          2,
				Sum:            1e300,
				Min:            metricdata.NewExtrema(1e-300),
				Max:            metricdata.NewExtrema(1e300),
				Scale:          -4,
				PositiveBucket: metricdata.ExponentialBucket{Offset: -63, Counts: wide},
			},
		},
		{
			name:    "default size negative",
			maxSize: 160,
			values:  []float64{-1e300, -1e-300},
			want: metricdata.ExponentialHistogramDataPoint[float64]{
				Count:          2,
				Sum:            -1e300,
				Min:            metricdata.NewExtrema(-1e300),
				Max:            metricdata.NewExtrema(-1e-300),
				Scale:          -4,
				NegativeBucket: metricdata.ExponentialBucket{Offset: -63, Counts: wide},
			},
		},
		{
			name:    "two buckets",
			maxSize: 2,
			values:  []float64{1e300, 1e-300},
			want: metricdata.ExponentialHistogramDataPoint[float64]{
				Count:          2,
				Sum:            1e300,
				Min:            metricdata.NewExtrema(1e-300),
				Max:            metricdata.NewExtrema(1e300),
				Scale:          -10,
				PositiveBucket: metricdata.ExponentialBucket{Offset: -1, Counts: []uint64{1, 1}},
			},
		},
		{
			// Scale -10 cannot go lower, so the third bucket is kept rather
			// than the observation lost.
			name:    "whole float64 range at the minimum scale",
			maxSize: 2,
			values:  []float64{math.SmallestNonzeroFloat64, 1, math.MaxFloat64},
			want: metricdata.ExponentialHistogramDataPoint[float64]{
				Count:          3,
				Sum:            math.MaxFloat64,
				Min:            metricdata.NewExtrema(math.SmallestNonzeroFloat64),
				Max:            metricdata.NewExtrema(math.MaxFloat64),
				Scale:          -10,
				PositiveBucket: metricdata.ExponentialBucket{Offset: -2, Counts: []uint64{1, 1, 1}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			h := newExpoHistogram(tt.maxSize, 20)

			// When
			recordAll(h, tt.values...)

			// Then
			assert.Equal(t, tt.want, h.snapshot())
		})
	}
}

func TestExpoInsertionOrderIndependent(t *testing.T) {
	// Given: values whose sum is 1e300 in any order, so the whole point compares exactly.
	values := []float64{
		0, math.Copysign(0, -1), 1, 2, 4, 3, 0.1, 7, -7, -3, -0.001, 1000,
		math.Nextafter(1, 2), math.Nextafter(2, 0), math.Nextafter(1, 0),
		1e-300, 1e300, math.SmallestNonzeroFloat64, 1e-5, -1e5, math.Pi,
	}
	for _, maxSize := range []int32{2, 8, 160} {
		t.Run(fmt.Sprint("maxSize ", maxSize), func(t *testing.T) {
			forward := newExpoHistogram(maxSize, 20)
			recordAll(forward, values...)
			want := forward.snapshot()

			orders := make([][]float64, 0, 21)
			reversed := slices.Clone(values)
			slices.Reverse(reversed)
			orders = append(orders, reversed)
			r := rand.New(rand.NewPCG(1, uint64(maxSize)))
			for range 20 {
				shuffled := slices.Clone(values)
				r.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
				orders = append(orders, shuffled)
			}

			for _, order := range orders {
				// When
				h := newExpoHistogram(maxSize, 20)
				recordAll(h, order...)

				// Then
				require.Equal(t, want, h.snapshot(), "order %v", order)
			}
		})
	}
}

func TestExpoIgnoresNaNAndInf(t *testing.T) {
	// Given
	h := newExpoHistogram(160, 20)

	// When
	recordAll(h, math.NaN(), math.Inf(1), math.Inf(-1))

	// Then
	assert.Equal(t, metricdata.ExponentialHistogramDataPoint[float64]{Scale: 20}, h.snapshot())

	// When
	recordAll(h, 2, math.NaN(), math.Inf(-1))

	// Then
	assert.Equal(t, metricdata.ExponentialHistogramDataPoint[float64]{
		Count:          1,
		Sum:            2,
		Min:            metricdata.NewExtrema(2.0),
		Max:            metricdata.NewExtrema(2.0),
		Scale:          20,
		PositiveBucket: metricdata.ExponentialBucket{Offset: 1<<20 - 1, Counts: []uint64{1}},
	}, h.snapshot())
}

func TestExpoSnapshotDeepCopy(t *testing.T) {
	// Given
	h := newExpoHistogram(160, 0)
	recordAll(h, 1, -1)
	first := h.snapshot()

	// When: recording into existing buckets and changing the returned counts
	recordAll(h, 1, -1)
	first.PositiveBucket.Counts[0] = 100
	first.NegativeBucket.Counts[0] = 100

	// Then
	second := h.snapshot()
	assert.Equal(t, []uint64{2}, second.PositiveBucket.Counts)
	assert.Equal(t, []uint64{2}, second.NegativeBucket.Counts)
	assert.Equal(t, []uint64{100}, first.PositiveBucket.Counts)
	assert.Equal(t, uint64(2), first.Count)
}

func TestExpoParameterClamping(t *testing.T) {
	tests := []struct {
		maxSize, maxScale int32
		wantSize          int32
		wantScale         int32
	}{
		{maxSize: 160, maxScale: 20, wantSize: 160, wantScale: 20},
		{maxSize: 2, maxScale: -10, wantSize: 2, wantScale: -10},
		{maxSize: 1, maxScale: 21, wantSize: 2, wantScale: 20},
		{maxSize: 0, maxScale: -11, wantSize: 2, wantScale: -10},
		{maxSize: -5, maxScale: math.MaxInt32, wantSize: 2, wantScale: 20},
		{maxSize: math.MaxInt32, maxScale: math.MinInt32, wantSize: math.MaxInt32, wantScale: -10},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.maxSize, " ", tt.maxScale), func(t *testing.T) {
			// When
			h := newExpoHistogram(tt.maxSize, tt.maxScale)

			// Then
			assert.Equal(t, tt.wantSize, h.maxSize)
			assert.Equal(t, tt.wantScale, h.snapshot().Scale)
		})
	}

	t.Run("clamped size still bounds the buckets", func(t *testing.T) {
		// Given
		h := newExpoHistogram(0, 30)

		// When
		recordAll(h, 1, 2, 4)

		// Then: at scale -1 the indexes are -1, 0 and 0.
		dp := h.snapshot()
		assert.Equal(t, int32(-1), dp.Scale)
		assert.Equal(t, metricdata.ExponentialBucket{Offset: -1, Counts: []uint64{1, 2}}, dp.PositiveBucket)
	})
}

func TestExpoIndex(t *testing.T) {
	tests := []struct {
		value float64
		scale int32
		want  int
	}{
		// Scale 0: bucket i is (2^i, 2^(i+1)], powers of two close their bucket.
		{value: 1, scale: 0, want: -1},
		{value: 2, scale: 0, want: 0},
		{value: 4, scale: 0, want: 1},
		{value: 3, scale: 0, want: 1},
		{value: 1.5, scale: 0, want: 0},
		{value: 0.5, scale: 0, want: -2},
		{value: 0.75, scale: 0, want: -1},
		{value: math.Nextafter(1, 2), scale: 0, want: 0},
		{value: math.Nextafter(1, 0), scale: 0, want: -1},
		{value: math.Nextafter(2, 0), scale: 0, want: 0},
		{value: 1e-300, scale: 0, want: -997},
		{value: 1e300, scale: 0, want: 996},
		{value: math.Ldexp(1, -1022), scale: 0, want: -1023},
		{value: math.SmallestNonzeroFloat64, scale: 0, want: -1075},
		{value: math.MaxFloat64, scale: 0, want: 1023},

		// Scale 1: base sqrt(2).
		{value: 1, scale: 1, want: -1},
		{value: 1.4, scale: 1, want: 0},
		{value: 1.5, scale: 1, want: 1},
		{value: 2, scale: 1, want: 1},
		{value: 2.5, scale: 1, want: 2},
		{value: 3, scale: 1, want: 3},
		{value: 4, scale: 1, want: 3},
		{value: 0.5, scale: 1, want: -3},

		// Scale 3: base 2^(1/8).
		{value: 1.05, scale: 3, want: 0},
		{value: 1.1, scale: 3, want: 1},
		{value: 1.9, scale: 3, want: 7},
		{value: 2, scale: 3, want: 7},
		{value: 0.9, scale: 3, want: -2},

		// Scale 20: neighbors of powers of two stay on their side.
		{value: 1, scale: 20, want: -1},
		{value: math.Nextafter(1, 2), scale: 20, want: 0},
		{value: math.Nextafter(1, 0), scale: 20, want: -1},
		{value: 2, scale: 20, want: 1<<20 - 1},
		{value: math.Nextafter(2, 0), scale: 20, want: 1<<20 - 1},
		{value: math.Nextafter(2, 4), scale: 20, want: 1 << 20},
		{value: 0.5, scale: 20, want: -1<<20 - 1},
		{value: math.MaxFloat64, scale: 20, want: 1024<<20 - 1},
		{value: math.SmallestNonzeroFloat64, scale: 20, want: -1074<<20 - 1},

		// Negative scales: base 4 at -1 and 2^1024 at -10.
		{value: 1, scale: -1, want: -1},
		{value: 2, scale: -1, want: 0},
		{value: 4, scale: -1, want: 0},
		{value: 5, scale: -1, want: 1},
		{value: 16, scale: -1, want: 1},
		{value: 17, scale: -1, want: 2},
		{value: 0.25, scale: -1, want: -2},
		{value: 1, scale: -10, want: -1},
		{value: 2, scale: -10, want: 0},
		{value: math.MaxFloat64, scale: -10, want: 0},
		{value: math.Ldexp(1, -1022), scale: -10, want: -1},
		{value: math.Ldexp(1, -1023), scale: -10, want: -1},
		{value: math.Ldexp(1, -1024), scale: -10, want: -2},
		{value: math.SmallestNonzeroFloat64, scale: -10, want: -2},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.value, " at ", tt.scale), func(t *testing.T) {
			assert.Equal(t, tt.want, expoIndex(tt.value, tt.scale))
		})
	}
}

// expoIndexValues are positive values around powers of two, extremes and
// arbitrary values in between.
var expoIndexValues = []float64{
	1, 2, 3, 0.1, 0.5, 0.75, 1.5, math.Pi, math.E, math.Sqrt2, 12345.678, 1e-10,
	1e-300, 1e300, math.MaxFloat64, math.SmallestNonzeroFloat64, math.Ldexp(1, -1022),
	math.Nextafter(1, 2), math.Nextafter(1, 0), math.Nextafter(2, 0), math.Nextafter(2, 4),
	math.Nextafter(math.Ldexp(1, 1000), 0), math.Nextafter(math.Ldexp(1, 1000), math.Inf(1)),
	math.Nextafter(math.Ldexp(1, -1000), 0), math.Nextafter(math.Ldexp(1, -1000), 1),
}

func TestExpoIndexDownscaleConsistent(t *testing.T) {
	// Downscaling by one merges bucket pairs: index(v, s-1) == index(v, s) >> 1.
	for _, v := range expoIndexValues {
		for scale := expoMaxScale; scale > expoMinScale; scale-- {
			assert.Equal(t, expoIndex(v, scale)>>1, expoIndex(v, scale-1), "%v at scale %d", v, scale)
		}
	}
}

func TestExpoIndexBucketHoldsValue(t *testing.T) {
	// At scales <= 0 the bucket bounds are exact powers of two:
	// 2^(i*2^-s) < v <= 2^((i+1)*2^-s).
	for _, v := range expoIndexValues {
		for scale := int32(0); scale >= expoMinScale; scale-- {
			index := expoIndex(v, scale)
			lower := math.Ldexp(1, index<<-scale)
			upper := math.Ldexp(1, (index+1)<<-scale)
			assert.Less(t, lower, v, "%v at scale %d", v, scale)
			assert.LessOrEqual(t, v, upper, "%v at scale %d", v, scale)
		}
	}
}
