package stats

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

func TestWithTemporality_setsOTLPTemporality(t *testing.T) {
	// Given
	config := DefaultConfig()

	// When
	WithTemporality(Delta)(config)

	// Then
	require.NotNil(t, config.OTLP)
	require.Equal(t, Delta, config.OTLP.Temporality)
}

func TestWithOTLP_preserves_explicit_resource_and_service_options(t *testing.T) {
	// Given
	config := DefaultConfig()
	WithServiceName("explicit-service")(config)
	WithEnvironment("production")(config)
	WithOTLPResourceAttributes(attribute.String("team", "payments"))(config)
	WithOTLPResourceSchemaURL("https://opentelemetry.io/schemas/1.26.0")(config)
	WithTemporality(Delta)(config)

	// When
	WithOTLP(&OTLPConfig{Endpoint: "localhost:4317"})(config)

	// Then
	require.Equal(t, "explicit-service", config.OTLP.ServiceName)
	require.Equal(t, "production", config.OTLP.DeploymentEnvironment)
	require.Equal(t, Delta, config.OTLP.Temporality)
	require.Equal(t, []attribute.KeyValue{attribute.String("team", "payments")}, config.OTLP.ResourceAttributes)
	require.Equal(t, "https://opentelemetry.io/schemas/1.26.0", config.OTLP.ResourceSchemaURL)
}

func TestWithOTLP_option_reused_for_two_clients_keeps_their_settings_apart(t *testing.T) {
	// Given: one WithOTLP option and two configs with different earlier options
	option := WithOTLP(&OTLPConfig{Endpoint: "localhost:4317"})
	first, second := DefaultConfig(), DefaultConfig()
	WithServiceName("first-service")(first)
	WithOTLPResourceAttributes(attribute.String("team", "first"))(first)
	WithServiceName("second-service")(second)

	// When
	option(first)
	option(second)

	// Then
	require.Equal(t, "first-service", first.OTLP.ServiceName)
	require.Equal(t, "second-service", second.OTLP.ServiceName)
	require.Empty(t, second.OTLP.ResourceAttributes)
}

func TestWithExponentialHistogram_sets_the_OTLP_exponential_settings(t *testing.T) {
	// Given
	config := DefaultConfig()

	// When
	WithExponentialHistogram(40, 8)(config)

	// Then
	require.NotNil(t, config.OTLP)
	require.Equal(t, &OTLPExponentialHistogram{MaxSize: 40, MaxScale: 8}, config.OTLP.ExponentialHistogram)
}

func TestWithExponentialHistogram_zero_selects_the_defaults(t *testing.T) {
	// Given
	config := DefaultConfig()

	// When
	WithExponentialHistogram(0, 0)(config)

	// Then
	require.Equal(t, OTLPExponentialHistogram{MaxSize: 160, MaxScale: 20}, config.OTLP.ExponentialHistogram.Resolved())
}

func TestWithExponentialHistogram_survives_WithOTLP_in_either_order(t *testing.T) {
	tests := map[string][]Option{
		"before WithOTLP": {WithExponentialHistogram(40, 8), WithOTLP(&OTLPConfig{Endpoint: "c:4317"})},
		"after WithOTLP":  {WithOTLP(&OTLPConfig{Endpoint: "c:4317"}), WithExponentialHistogram(40, 8)},
	}
	for name, options := range tests {
		t.Run(name, func(t *testing.T) {
			// Given
			config := DefaultConfig()

			// When
			for _, option := range options {
				option(config)
			}

			// Then
			require.Equal(t, "c:4317", config.OTLP.Endpoint)
			require.Equal(t, &OTLPExponentialHistogram{MaxSize: 40, MaxScale: 8}, config.OTLP.ExponentialHistogram)
		})
	}
}

func TestWithExponentialHistogram_settings_passed_to_WithOTLP_are_copied(t *testing.T) {
	// Given
	settings := &OTLPExponentialHistogram{MaxSize: 40, MaxScale: 8}
	config := DefaultConfig()
	WithOTLP(&OTLPConfig{Endpoint: "c:4317", ExponentialHistogram: settings})(config)

	// When: the caller reuses its struct
	settings.MaxSize = 1

	// Then
	require.Equal(t, &OTLPExponentialHistogram{MaxSize: 40, MaxScale: 8}, config.OTLP.ExponentialHistogram)
}

func TestWithExponentialHistogram_invalid_settings_return_ErrInvalidConfig(t *testing.T) {
	tests := map[string]struct{ maxSize, maxScale int32 }{
		"max size 1 and scale 30": {1, 30},
		"max size 1":              {1, 20},
		"negative max size":       {-160, 20},
		"scale above 20":          {160, 21},
		"scale below -10":         {160, -11},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			// When
			client, err := NewClient(WithExponentialHistogram(tt.maxSize, tt.maxScale))

			// Then
			require.ErrorIs(t, err, ErrInvalidConfig)
			require.Nil(t, client)
			t.Log(err)
		})
	}
}

func TestWithExponentialHistogram_accepts_the_limits_and_the_defaults(t *testing.T) {
	for _, settings := range []struct{ maxSize, maxScale int32 }{{2, -10}, {160, 20}, {0, 0}} {
		// When
		client, err := NewClient(WithExponentialHistogram(settings.maxSize, settings.maxScale))

		// Then
		require.NoError(t, err, "maxSize %d, maxScale %d", settings.maxSize, settings.maxScale)
		require.NoError(t, client.Close())
	}
}
