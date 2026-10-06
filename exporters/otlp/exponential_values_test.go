package otlp

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

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
