package otlp

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

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
