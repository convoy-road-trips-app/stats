package models

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOTLPExponentialHistogram_Resolved_replaces_zero_fields_with_the_defaults(t *testing.T) {
	tests := []struct {
		name     string
		settings OTLPExponentialHistogram
		want     OTLPExponentialHistogram
	}{
		{"zero value", OTLPExponentialHistogram{}, OTLPExponentialHistogram{MaxSize: 160, MaxScale: 20}},
		{"zero max size", OTLPExponentialHistogram{MaxScale: -3}, OTLPExponentialHistogram{MaxSize: 160, MaxScale: -3}},
		{"zero max scale", OTLPExponentialHistogram{MaxSize: 40}, OTLPExponentialHistogram{MaxSize: 40, MaxScale: 20}},
		{"both set", OTLPExponentialHistogram{MaxSize: 2, MaxScale: -10}, OTLPExponentialHistogram{MaxSize: 2, MaxScale: -10}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When
			got := tt.settings.Resolved()

			// Then
			require.Equal(t, tt.want, got)
		})
	}
}

func TestOTLPExponentialHistogram_Validate_checks_the_resolved_limits(t *testing.T) {
	tests := []struct {
		name     string
		settings OTLPExponentialHistogram
		wantErr  string
	}{
		{name: "defaults", settings: OTLPExponentialHistogram{}},
		{name: "smallest size and scale", settings: OTLPExponentialHistogram{MaxSize: 2, MaxScale: -10}},
		{name: "finest scale", settings: OTLPExponentialHistogram{MaxSize: 160, MaxScale: 20}},
		{name: "max size 1", settings: OTLPExponentialHistogram{MaxSize: 1, MaxScale: 20}, wantErr: "max size 1"},
		{name: "negative max size", settings: OTLPExponentialHistogram{MaxSize: -160}, wantErr: "max size -160"},
		{name: "scale above 20", settings: OTLPExponentialHistogram{MaxScale: 21}, wantErr: "max scale 21"},
		{name: "scale below -10", settings: OTLPExponentialHistogram{MaxScale: -11}, wantErr: "max scale -11"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When
			err := tt.settings.Validate()

			// Then
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestOTLPConfig_Validate_rejects_invalid_exponential_histogram_settings(t *testing.T) {
	// Given: OTLP is not enabled, as with WithExponentialHistogram alone
	config := OTLPConfig{ExponentialHistogram: &OTLPExponentialHistogram{MaxSize: 1, MaxScale: 30}}

	// When
	err := config.Validate()

	// Then
	require.ErrorContains(t, err, "exponential histogram")
}
