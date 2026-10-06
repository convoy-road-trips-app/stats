package stats

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

func TestNewClient_OTLP_resource_uses_environment_when_identity_is_not_explicit(t *testing.T) {
	// Given: a default client (no WithServiceName/WithEnvironment) and deployment env vars.
	received := make(chan *collectormetricspb.ExportMetricsServiceRequest, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var request collectormetricspb.ExportMetricsServiceRequest
		if err := proto.Unmarshal(body, &request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		received <- &request
	}))
	defer server.Close()
	t.Setenv("OTEL_SERVICE_NAME", "checkout-api")
	t.Setenv("DEPLOYMENT_ENVIRONMENT", "production")
	t.Setenv("SERVICE_VERSION", "2.4.1")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "team=payments")
	client, err := NewClient(WithOTLP(&OTLPConfig{
		Endpoint: strings.TrimPrefix(server.URL, "http://"),
		Insecure: true,
		Protocol: OTLPProtocolHTTP,
	}))
	require.NoError(t, err)

	// When
	require.NoError(t, client.Counter(context.Background(), "requests_total", 1))
	defer func() { require.NoError(t, client.Shutdown(context.Background())) }()

	// Then
	host, err := os.Hostname()
	require.NoError(t, err)
	var request *collectormetricspb.ExportMetricsServiceRequest
	select {
	case request = <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("OTLP receiver got no export within 5s")
	}
	attrs := make(map[string]string)
	for _, attr := range request.GetResourceMetrics()[0].GetResource().GetAttributes() {
		attrs[attr.GetKey()] = attr.GetValue().GetStringValue()
	}
	// Detected host, process and SDK attributes are present by default.
	require.Equal(t, host, attrs["host.name"])
	require.Equal(t, "go", attrs["process.runtime.name"])
	require.Equal(t, "opentelemetry", attrs["telemetry.sdk.name"])
	for key := range attrs {
		if strings.HasPrefix(key, "process.") || strings.HasPrefix(key, "telemetry.sdk.") || key == "host.name" {
			delete(attrs, key)
		}
	}
	require.Equal(t, map[string]string{
		"service.name":           "checkout-api",
		"deployment.environment": "production",
		"service.version":        "2.4.1",
		"team":                   "payments",
		"service.instance.id":    host,
	}, attrs)
}

func TestWithoutOTLPResourceDetection(t *testing.T) {
	cfg := newConfigFrom(t, WithoutOTLPResourceDetection(), WithOTLP(&OTLPConfig{Endpoint: "c:4317"}))
	require.True(t, cfg.OTLP.DisableResourceDetection, "WithOTLP keeps an earlier dedicated opt-out")
	require.False(t, newConfigFrom(t, WithOTLP(&OTLPConfig{Endpoint: "c:4317"})).OTLP.DisableResourceDetection)
}
