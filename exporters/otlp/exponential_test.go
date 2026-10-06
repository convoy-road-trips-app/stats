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
