package stats

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// boundsReceiver collects the explicit bounds of every histogram it receives.
func boundsReceiver(t *testing.T) (endpoint string, bounds func() map[string][]float64) {
	t.Helper()
	var mu sync.Mutex
	got := map[string][]float64{}
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
		mu.Lock()
		defer mu.Unlock()
		for _, rm := range request.GetResourceMetrics() {
			for _, sm := range rm.GetScopeMetrics() {
				for _, m := range sm.GetMetrics() {
					if points := m.GetHistogram().GetDataPoints(); len(points) > 0 {
						got[m.GetName()] = points[0].GetExplicitBounds()
					}
				}
			}
		}
	}))
	t.Cleanup(server.Close)
	return strings.TrimPrefix(server.URL, "http://"), func() map[string][]float64 {
		mu.Lock()
		defer mu.Unlock()
		out := make(map[string][]float64, len(got))
		for name, b := range got {
			out[name] = b
		}
		return out
	}
}

func TestNewClient_OTLP_uses_per_metric_buckets_and_copies_them(t *testing.T) {
	// Given: per-name bounds from a caller-owned slice, and global bounds
	endpoint, received := boundsReceiver(t)
	perName := []float64{100, 200, 400}
	client, err := NewClient(
		WithFlushInterval(time.Hour),
		WithOTLP(&OTLPConfig{Endpoint: endpoint, Insecure: true, Protocol: OTLPProtocolHTTP}),
		WithHistogramBuckets([]float64{1, 10}),
		WithHistogramBucketsFor("a_latency_ms", perName...),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, client.Shutdown(context.Background())) }()

	// When: the caller reuses its slice and the client config after construction
	perName[0] = 999
	client.core.cfg.HistogramBucketsByName["a_latency_ms"][1] = 999
	ctx := context.Background()
	require.NoError(t, client.Histogram(ctx, "a_latency_ms", 150))
	require.NoError(t, client.Histogram(ctx, "b_latency_ms", 5))
	require.NoError(t, client.Flush(ctx))

	// Then
	require.Eventually(t, func() bool { return len(received()) == 2 }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, []float64{100, 200, 400}, received()["a_latency_ms"])
	require.Equal(t, []float64{1, 10}, received()["b_latency_ms"])
}
