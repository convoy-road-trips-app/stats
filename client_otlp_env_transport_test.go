package stats

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

type receivedExport struct {
	path     string
	token    string
	encoding string
	request  *collectormetricspb.ExportMetricsServiceRequest
}

// TestClient_OTLPEnvTransportReachesCollector proves the environment-resolved
// endpoint path, headers and compression take effect on the wire.
func TestClient_OTLPEnvTransportReachesCollector(t *testing.T) {
	received := make(chan receivedExport, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err == nil && r.Header.Get("Content-Encoding") == "gzip" {
			var reader *gzip.Reader
			if reader, err = gzip.NewReader(bytes.NewReader(body)); err == nil {
				body, err = io.ReadAll(reader)
			}
		}
		var request collectormetricspb.ExportMetricsServiceRequest
		if err == nil {
			err = proto.Unmarshal(body, &request)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		received <- receivedExport{r.URL.Path, r.Header.Get("x-token"), r.Header.Get("Content-Encoding"), &request}
	}))
	t.Cleanup(server.Close)
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", server.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_COMPRESSION", "gzip")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "x-token=s%20ecret")
	t.Setenv("OTEL_EXPORTER_OTLP_TIMEOUT", "2000")

	client, err := NewClient(WithServiceName("svc"), WithOTLPFromEnv())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, client.Counter(ctx, "env.requests", 1))
	require.NoError(t, client.Flush(ctx))

	select {
	case got := <-received:
		require.Equal(t, "/v1/metrics", got.path)
		require.Equal(t, "s ecret", got.token)
		require.Equal(t, "gzip", got.encoding)
		require.NotEmpty(t, got.request.GetResourceMetrics())
	case <-time.After(5 * time.Second):
		require.FailNow(t, "no export reached the collector")
	}
	require.NoError(t, client.Shutdown(ctx))
}
