package otlp

import (
	"os"
	"runtime"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats/models"
)

func resourceSet(config *models.OTLPConfig) attribute.Set {
	return attribute.NewSet(resourceForConfig(config).Attributes()...)
}

func TestResourceDetectsHostProcessAndSDK(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	host, err := os.Hostname()
	require.NoError(t, err)

	attrs := resourceSet(&models.OTLPConfig{})

	assertAttributeValue(t, attrs, "host.name", host)
	pid, ok := attrs.Value("process.pid")
	require.True(t, ok)
	require.Equal(t, int64(os.Getpid()), pid.AsInt64(), "process.pid %s", strconv.Itoa(os.Getpid()))
	assertAttributeValue(t, attrs, "process.runtime.name", "go")
	assertAttributeValue(t, attrs, "process.runtime.version", runtime.Version())
	assertAttributeValue(t, attrs, "telemetry.sdk.name", "opentelemetry")
	assertAttributeValue(t, attrs, "telemetry.sdk.language", "go")
	_, ok = attrs.Value("telemetry.sdk.version")
	require.True(t, ok)
}

func TestResourceDetectionDoesNotLeakSensitiveProcessData(t *testing.T) {
	attrs := resourceSet(&models.OTLPConfig{})

	for _, key := range []string{"process.command_args", "process.command_line", "process.owner", "process.executable.path"} {
		_, ok := attrs.Value(attribute.Key(key))
		require.False(t, ok, key)
	}
}

func TestResourceEnvAttributesBeatDetected(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "host.name=env-host,process.runtime.name=env-runtime")

	attrs := resourceSet(&models.OTLPConfig{})

	assertAttributeValue(t, attrs, "host.name", "env-host")
	assertAttributeValue(t, attrs, "process.runtime.name", "env-runtime")
	assertAttributeValue(t, attrs, "telemetry.sdk.language", "go")
}

func TestResourceExplicitAttributesBeatDetected(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "host.name=env-host")

	attrs := resourceSet(&models.OTLPConfig{ResourceAttributes: []attribute.KeyValue{
		attribute.String("host.name", "explicit-host"),
		attribute.Int("process.pid", 7),
	}})

	assertAttributeValue(t, attrs, "host.name", "explicit-host")
	pid, _ := attrs.Value("process.pid")
	require.Equal(t, int64(7), pid.AsInt64())
}

func TestResourceDetectionCanBeDisabled(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")

	attrs := resourceSet(&models.OTLPConfig{DisableResourceDetection: true})

	for _, key := range []string{"host.name", "process.pid", "process.runtime.name", "telemetry.sdk.name"} {
		_, ok := attrs.Value(attribute.Key(key))
		require.False(t, ok, key)
	}
	_, ok := attrs.Value("service.instance.id")
	require.True(t, ok, "service.instance.id is not part of detection")
}

func TestResourceDetectionKeepsServiceInstanceID(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	host, err := os.Hostname()
	require.NoError(t, err)

	assertAttributeValue(t, resourceSet(&models.OTLPConfig{}), "service.instance.id", host)
}
