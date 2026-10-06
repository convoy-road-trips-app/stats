package otlp

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func recorded(maxSize, maxScale int32, values ...float64) *expoHistogram {
	h := newExpoHistogram(maxSize, maxScale)
	recordAll(h, values...)
	return h
}

// requireSameHistogram compares everything exactly except the sum, which is
// compared with a tolerance because float addition order matters.
func requireSameHistogram(t *testing.T, want, got *expoHistogram) {
	t.Helper()
	w, g := want.snapshot(), got.snapshot()
	assert.InDelta(t, w.Sum, g.Sum, math.Abs(w.Sum)*1e-9+1e-300)
	w.Sum, g.Sum = 0, 0
	require.Equal(t, w, g)
	assert.Equal(t, want.maxSize, got.maxSize)
}

func TestExpoMergeDifferentScales(t *testing.T) {
	// Given: a stays at scale 20 until 4 forces it down, b ends at another scale.
	a := recorded(3, 20, 1, 2, 4)
	b := recorded(8, 20, 1.5, 3)
	require.NotEqual(t, a.scale, b.scale)

	// When / Then
	want := recorded(3, 20, 1, 2, 4, 1.5, 3)
	requireSameHistogram(t, want, mergeExpo(a, b))
	requireSameHistogram(t, want, mergeExpo(b, a))
}

func TestExpoMergeDisjointRangesWithGap(t *testing.T) {
	// Given
	a := recorded(160, 5, 0.001, 0.002, -0.003)
	b := recorded(160, 5, 1000, 2000, -5000)

	// When
	got := mergeExpo(a, b)

	// Then
	requireSameHistogram(t, recorded(160, 5, 0.001, 0.002, -0.003, 1000, 2000, -5000), got)
	zeros := 0
	for _, c := range got.positive.counts {
		if c == 0 {
			zeros++
		}
	}
	assert.NotZero(t, zeros, "the gap between the ranges is filled with zero counts")
}

func TestExpoMergeEmpty(t *testing.T) {
	// Given
	a := recorded(16, 8, 1, 2, -3, 0)
	empty := newExpoHistogram(16, 8)

	// When / Then
	requireSameHistogram(t, a, mergeExpo(a, empty))
	requireSameHistogram(t, a, mergeExpo(empty, a))
	requireSameHistogram(t, empty, mergeExpo(empty, newExpoHistogram(16, 8)))
	assert.Zero(t, mergeExpo(empty, empty).snapshot().Count)
}

func TestExpoMergeZeroOnly(t *testing.T) {
	// Given
	a := recorded(16, 8, 0, 0)
	b := recorded(16, 8, math.Copysign(0, -1))

	// When
	got := mergeExpo(a, b)

	// Then
	requireSameHistogram(t, recorded(16, 8, 0, 0, 0), got)
	dp := got.snapshot()
	assert.Equal(t, uint64(3), dp.ZeroCount)
	assert.Empty(t, dp.PositiveBucket.Counts)
	assert.Empty(t, dp.NegativeBucket.Counts)
}

func TestExpoMergePositiveOnlyAndNegativeOnly(t *testing.T) {
	// Given
	a := recorded(16, 10, 1, 2, 3, 100)
	b := recorded(16, 10, -0.5, -7, -9)

	// When / Then
	want := recorded(16, 10, 1, 2, 3, 100, -0.5, -7, -9)
	requireSameHistogram(t, want, mergeExpo(a, b))
	requireSameHistogram(t, want, mergeExpo(b, a))
}

func TestExpoMergeMinMax(t *testing.T) {
	// Given
	a := recorded(16, 8, 5, 6)
	b := recorded(16, 8, -2, 3)
	empty := newExpoHistogram(16, 8)

	// When
	dp := mergeExpo(a, b).snapshot()

	// Then
	assert.Equal(t, metricdata.NewExtrema(-2.0), dp.Min)
	assert.Equal(t, metricdata.NewExtrema(6.0), dp.Max)

	// An empty histogram's zero min and max do not leak into the result.
	dp = mergeExpo(a, empty).snapshot()
	assert.Equal(t, metricdata.NewExtrema(5.0), dp.Min)
	assert.Equal(t, metricdata.NewExtrema(6.0), dp.Max)
}

func TestExpoMergeExtremes(t *testing.T) {
	tests := []struct {
		name string
		a, b []float64
	}{
		{"1e-300 and 1e300", []float64{1e-300, -1e-300}, []float64{1e300, -1e300}},
		{"smallest and largest", []float64{math.SmallestNonzeroFloat64}, []float64{math.MaxFloat64}},
		{"negative smallest and largest", []float64{-math.SmallestNonzeroFloat64, 1}, []float64{-math.MaxFloat64, 3}},
	}
	for _, maxSize := range []int32{2, 4, 160} {
		for i := range tests {
			tt := &tests[i]
			t.Run(tt.name, func(t *testing.T) {
				// Given
				a := recorded(maxSize, 20, tt.a...)
				b := recorded(maxSize, 20, tt.b...)
				want := recorded(maxSize, 20, append(append([]float64{}, tt.a...), tt.b...)...)

				// When / Then
				requireSameHistogram(t, want, mergeExpo(a, b))
				requireSameHistogram(t, want, mergeExpo(b, a))
			})
		}
	}
}

func dyadicValues(rng *rand.Rand, n int) []float64 {
	values := make([]float64, n)
	for i := range values {
		k := rng.IntN(2<<14+1) - 1<<14
		values[i] = math.Ldexp(float64(k), -12)
	}
	return values
}

func TestExpoMergePropertyMatchesRecordAll(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	sizes := []int32{2, 3, 4, 7, 16, 160}

	for iter := range 300 {
		// Given: values split at random across parts with their own maxSize.
		values := dyadicValues(rng, 1+rng.IntN(60))
		parts := 1 + rng.IntN(4)
		partValues := make([][]float64, parts)
		for _, v := range values {
			p := rng.IntN(parts)
			partValues[p] = append(partValues[p], v)
		}
		hists := make([]*expoHistogram, parts)
		minSize := int32(math.MaxInt32)
		for i := range hists {
			size := sizes[rng.IntN(len(sizes))]
			minSize = min(minSize, size)
			hists[i] = recorded(size, 20, partValues[i]...)
		}

		// When: fold in random order.
		rng.Shuffle(len(hists), func(i, j int) { hists[i], hists[j] = hists[j], hists[i] })
		folded := hists[0]
		for _, h := range hists[1:] {
			if rng.IntN(2) == 0 {
				folded = mergeExpo(folded, h)
			} else {
				folded = mergeExpo(h, folded)
			}
		}

		// Then
		rng.Shuffle(len(values), func(i, j int) { values[i], values[j] = values[j], values[i] })
		want := recorded(minSize, 20, values...)
		w, g := want.snapshot(), folded.snapshot()
		require.Equal(t, w, g, "iteration %d", iter)
	}
}

func TestExpoMergeAccumulatorStyleFold(t *testing.T) {
	// Given: an accumulator that merges each interval's snapshot into its state.
	rng := rand.New(rand.NewPCG(3, 4))
	const maxSize = 8
	all := make([]float64, 0, 25*10)
	state := newExpoHistogram(maxSize, 20)

	for range 25 {
		interval := dyadicValues(rng, 1+rng.IntN(10))
		all = append(all, interval...)

		// When
		snap := recorded(maxSize, 20, interval...).snapshot()
		state = mergeExpo(state, expoHistogramFromPoint(&snap, maxSize))
		stateSnap := state.snapshot()
		state = expoHistogramFromPoint(&stateSnap, maxSize)
	}

	// Then
	assert.Equal(t, recorded(maxSize, 20, all...).snapshot(), state.snapshot())
}

func TestExpoMergeReappliesMaxSize(t *testing.T) {
	// Given: two wide histograms merged under a small limit.
	a := recorded(160, 20, 1, 1.5, 2, 3)
	b := recorded(160, 20, 8, 16, 1000)
	snap := a.snapshot()
	require.Greater(t, len(snap.PositiveBucket.Counts), 4)

	// When: FromPoint re-applies maxSize 4 to a, then merges with b.
	small := expoHistogramFromPoint(&snap, 4)
	got := mergeExpo(small, b)

	// Then
	assert.Equal(t, int32(4), got.maxSize)
	assert.LessOrEqual(t, len(got.positive.counts), 4)
	requireSameHistogram(t, recorded(4, 20, 1, 1.5, 2, 3, 8, 16, 1000), got)
}

func TestExpoHistogramFromPointRoundTrip(t *testing.T) {
	// Given
	h := recorded(16, 12, 0, 1, 2.5, -3, -40, 1000, 0.01)
	want := h.snapshot()

	// When
	rebuilt := expoHistogramFromPoint(&want, 16)

	// Then
	assert.Equal(t, want, rebuilt.snapshot())
	assert.Equal(t, h.scale, rebuilt.scale)

	// And the copy is deep: changing it leaves the data point alone.
	rebuilt.record(5000)
	rebuilt.record(-0.0001)
	assert.Equal(t, want, h.snapshot())
	require.NotEmpty(t, want.PositiveBucket.Counts)
	before := want.PositiveBucket.Counts[0]
	rebuilt.record(0.01)
	assert.Equal(t, before, want.PositiveBucket.Counts[0])
}

func TestExpoHistogramFromPointEmpty(t *testing.T) {
	// Given
	snap := newExpoHistogram(16, 6).snapshot()

	// When
	h := expoHistogramFromPoint(&snap, 16)

	// Then
	assert.Equal(t, snap, h.snapshot())
}

func TestExpoMergeDoesNotMutateOrAlias(t *testing.T) {
	// Given
	a := recorded(160, 20, 1, 2, 4, -3)
	b := recorded(160, 20, 1.5, 3, 100)
	aSnap, bSnap := a.snapshot(), b.snapshot()

	// When
	got := mergeExpo(a, b)
	got.record(7)
	got.record(-9)
	got.record(1e6)
	got.positive.counts[0] += 10

	// Then
	assert.Equal(t, aSnap, a.snapshot())
	assert.Equal(t, bSnap, b.snapshot())
	assert.NotSame(t, a, got)
	assert.NotSame(t, b, got)
}
