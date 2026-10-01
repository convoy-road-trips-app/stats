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
