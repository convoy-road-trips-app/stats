package otlp

import (
	"os"
	"sort"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"

	"github.com/convoy-road-trips-app/stats/models"
)

func metricTemporality(temporality models.Temporality) metricdata.Temporality {
	if temporality == models.Delta {
		return metricdata.DeltaTemporality
	}
	return metricdata.CumulativeTemporality
}

func resourceForConfig(config *models.OTLPConfig) *resource.Resource {
	attrs := make(map[string]attribute.KeyValue)
	for _, entry := range parseResourceAttributes(os.Getenv("OTEL_RESOURCE_ATTRIBUTES")) {
		attrs[string(entry.Key)] = entry
	}
	// Dedicated environment variables override OTEL_RESOURCE_ATTRIBUTES; explicit config is applied last.
	applyEnvironmentAttribute(attrs, "service.name", "OTEL_SERVICE_NAME")
	applyEnvironmentAttribute(attrs, "deployment.environment", "DEPLOYMENT_ENVIRONMENT")
	applyEnvironmentAttribute(attrs, "service.version", "SERVICE_VERSION")
	for _, entry := range config.ResourceAttributes {
		attrs[string(entry.Key)] = entry
	}
	if config.ServiceName != "" {
		attrs["service.name"] = attribute.String("service.name", config.ServiceName)
	} else if _, ok := attrs["service.name"]; !ok {
		attrs["service.name"] = attribute.String("service.name", "unknown_service")
	}
	if config.DeploymentEnvironment != "" {
		attrs["deployment.environment"] = attribute.String("deployment.environment", config.DeploymentEnvironment)
	} else if _, ok := attrs["deployment.environment"]; !ok {
		attrs["deployment.environment"] = attribute.String("deployment.environment", "unknown")
	}
	if config.ServiceVersion != "" {
		attrs["service.version"] = attribute.String("service.version", config.ServiceVersion)
	} else if _, ok := attrs["service.version"]; !ok {
		attrs["service.version"] = attribute.String("service.version", "unknown")
	}
	keys := make([]string, 0, len(attrs))
	for key := range attrs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]attribute.KeyValue, 0, len(keys))
	for _, key := range keys {
		values = append(values, attrs[key])
	}
	return resource.NewWithAttributes("", values...)
}

func applyEnvironmentAttribute(attrs map[string]attribute.KeyValue, key, env string) {
	if value, ok := os.LookupEnv(env); ok && value != "" {
		attrs[key] = attribute.String(key, value)
	}
}

func parseResourceAttributes(raw string) []attribute.KeyValue {
	entries := strings.Split(raw, ",")
	attrs := make([]attribute.KeyValue, 0, len(entries))
	for _, entry := range entries {
		key, value, ok := strings.Cut(strings.TrimSpace(entry), "=")
		if !ok || key == "" {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		attrs = append(attrs, attribute.String(key, strings.TrimSpace(value)))
	}
	return attrs
}
