package otlp

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/convoy-road-trips-app/stats/models"
)

func TestToResourceMetricsWithConfig_DeltaTemporality(t *testing.T) {
	// Given
	config := &models.OTLPConfig{Temporality: models.Delta}
	metrics := []*models.Metric{
		{Name: "counter", Type: models.MetricTypeCounter, Value: 1},
		{Name: "histogram", Type: models.MetricTypeHistogram, Value: 1},
	}

	// When
	rm := toResourceMetricsWithConfig(config, metrics, models.DefaultHistogramBuckets())

	// Then
	assert.Equal(t, metricdata.DeltaTemporality, rm.ScopeMetrics[0].Metrics[0].Data.(metricdata.Sum[float64]).Temporality)
	assert.Equal(t, metricdata.DeltaTemporality, rm.ScopeMetrics[0].Metrics[1].Data.(metricdata.Histogram[float64]).Temporality)
}

func TestToResourceMetricsWithConfig_EnvironmentResourceAttributes(t *testing.T) {
	// Given
	t.Setenv("OTEL_SERVICE_NAME", "env-service")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "team=payments,service.name=attribute-service,deployment.environment=attribute-env,service.version=attribute-version")
	t.Setenv("DEPLOYMENT_ENVIRONMENT", "production")
	t.Setenv("SERVICE_VERSION", "2.4.1")
	config := &models.OTLPConfig{}

	// When
	rm := toResourceMetricsWithConfig(config, nil, models.DefaultHistogramBuckets())

	// Then
	attrs := attribute.NewSet(rm.Resource.Attributes()...)
	assertAttributeValue(t, attrs, "service.name", "env-service")
	assertAttributeValue(t, attrs, "deployment.environment", "production")
	assertAttributeValue(t, attrs, "service.version", "2.4.1")
	assertAttributeValue(t, attrs, "team", "payments")
}

func TestToResourceMetricsWithConfig_ExplicitConfigOverridesEnvironment(t *testing.T) {
	// Given
	t.Setenv("OTEL_SERVICE_NAME", "env-service")
	t.Setenv("DEPLOYMENT_ENVIRONMENT", "production")
	t.Setenv("SERVICE_VERSION", "2.4.1")
	config := &models.OTLPConfig{
		ServiceName:           "configured-service",
		DeploymentEnvironment: "configured-env",
		ServiceVersion:        "1.0.0",
	}

	// When
	rm := toResourceMetricsWithConfig(config, nil, models.DefaultHistogramBuckets())

	// Then
	attrs := attribute.NewSet(rm.Resource.Attributes()...)
	assertAttributeValue(t, attrs, "service.name", "configured-service")
	assertAttributeValue(t, attrs, "deployment.environment", "configured-env")
	assertAttributeValue(t, attrs, "service.version", "1.0.0")
}

func TestToResourceMetricsWithResourceAttributes_MergesAttributes(t *testing.T) {
	// Given
	config := &models.OTLPConfig{
		ResourceAttributes: []attribute.KeyValue{
			attribute.String("team", "payments"),
			attribute.String("service.name", "resource-service"),
			attribute.String("deployment.environment", "resource-env"),
			attribute.String("service.version", "resource-version"),
		},
	}

	// When
	rm := toResourceMetricsWithConfig(config, nil, models.DefaultHistogramBuckets())

	// Then
	attrs := attribute.NewSet(rm.Resource.Attributes()...)
	assertAttributeValue(t, attrs, "team", "payments")
	assertAttributeValue(t, attrs, "service.name", "resource-service")
	assertAttributeValue(t, attrs, "deployment.environment", "resource-env")
	assertAttributeValue(t, attrs, "service.version", "resource-version")
}

func TestParseResourceAttributes_splitsCommaSeparatedEntries(t *testing.T) {
	// Given
	raw := "team=payments,service.version=2.4.1"

	// When
	attrs := attribute.NewSet(parseResourceAttributes(raw)...)

	// Then
	assertAttributeValue(t, attrs, "team", "payments")
	assertAttributeValue(t, attrs, "service.version", "2.4.1")
}

func TestParseResourceAttributes_percent_decodes_values_like_the_OTel_SDK(t *testing.T) {
	// Given: W3C Baggage percent-encoding, and a malformed escape the SDK keeps as is
	raw := "service.name=checkout%20api,team=pay%2Cments,discount=50%"

	// When
	attrs := attribute.NewSet(parseResourceAttributes(raw)...)

	// Then
	assertAttributeValue(t, attrs, "service.name", "checkout api")
	assertAttributeValue(t, attrs, "team", "pay,ments")
	assertAttributeValue(t, attrs, "discount", "50%")
}

func TestToResourceMetricsWithConfig_DefaultServiceInstanceIDIsHostname(t *testing.T) {
	// Given
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	host, err := os.Hostname()
	require.NoError(t, err)

	// When
	rm := toResourceMetricsWithConfig(&models.OTLPConfig{}, nil, models.DefaultHistogramBuckets())

	// Then
	assertAttributeValue(t, attribute.NewSet(rm.Resource.Attributes()...), "service.instance.id", host)
}

func TestToResourceMetricsWithConfig_ServiceInstanceIDFromEnvironment(t *testing.T) {
	// Given
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.instance.id=task-from-env")

	// When
	rm := toResourceMetricsWithConfig(&models.OTLPConfig{}, nil, models.DefaultHistogramBuckets())

	// Then
	assertAttributeValue(t, attribute.NewSet(rm.Resource.Attributes()...), "service.instance.id", "task-from-env")
}

func TestToResourceMetricsWithConfig_ServiceInstanceIDFromConfigOverridesEnvironment(t *testing.T) {
	// Given
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.instance.id=task-from-env")
	config := &models.OTLPConfig{ResourceAttributes: []attribute.KeyValue{attribute.String("service.instance.id", "task-from-config")}}

	// When
	rm := toResourceMetricsWithConfig(config, nil, models.DefaultHistogramBuckets())

	// Then
	assertAttributeValue(t, attribute.NewSet(rm.Resource.Attributes()...), "service.instance.id", "task-from-config")
}

func assertAttributeValue(t *testing.T, attrs attribute.Set, key, want string) {
	t.Helper()
	value, ok := attrs.Value(attribute.Key(key))
	require.True(t, ok, "resource attribute %q must be present", key)
	assert.Equal(t, want, value.AsString())
}
