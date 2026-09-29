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
	WithTemporality(Delta)(config)

	// When
	WithOTLP(&OTLPConfig{Endpoint: "localhost:4317"})(config)

	// Then
	require.Equal(t, "explicit-service", config.OTLP.ServiceName)
	require.Equal(t, "production", config.OTLP.DeploymentEnvironment)
	require.Equal(t, Delta, config.OTLP.Temporality)
	require.Equal(t, []attribute.KeyValue{attribute.String("team", "payments")}, config.OTLP.ResourceAttributes)
}
