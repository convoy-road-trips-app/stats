package stats

import (
	"context"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// metricsReceiver is an OTLP/HTTP receiver that keeps the latest metric it
// received under each name.
func metricsReceiver(t *testing.T) (endpoint string, latest func() map[string]*metricspb.Metric) {
	t.Helper()
	var mu sync.Mutex
	got := map[string]*metricspb.Metric{}
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
					got[m.GetName()] = m
				}
			}
		}
	}))
	t.Cleanup(server.Close)
	return strings.TrimPrefix(server.URL, "http://"), func() map[string]*metricspb.Metric {
		mu.Lock()
		defer mu.Unlock()
		return maps.Clone(got)
	}
}

func TestWithExponentialHistogram_exports_exponential_histograms_except_per_name_buckets(t *testing.T) {
	// Given: exponential histograms of at most 4 buckets per range, global
	// bounds, and explicit bounds for one metric
	endpoint, received := metricsReceiver(t)
	client, err := NewClient(
		WithFlushInterval(time.Hour),
		WithVersionReporting(false),
		WithOTLP(&OTLPConfig{Endpoint: endpoint, Insecure: true, Protocol: OTLPProtocolHTTP}),
		WithHistogramBuckets([]float64{1, 10}),
		WithHistogramBucketsFor("explicit_ms", 100, 200),
		WithExponentialHistogram(4, 20),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Shutdown(context.Background())) })

	// When
	ctx := context.Background()
	for _, value := range []float64{1, 2, 4} {
		require.NoError(t, client.Histogram(ctx, "latency", value))
	}
	require.NoError(t, client.Histogram(ctx, "explicit_ms", 150))
	require.NoError(t, client.Flush(ctx))

	// Then: latency is exponential, at scale 0 to fit 4 buckets; explicit_ms
	// keeps its own bounds
	require.Eventually(t, func() bool { return len(received()) == 2 }, 5*time.Second, 10*time.Millisecond)
	latency := received()["latency"]
	require.Nil(t, latency.GetHistogram())
	points := latency.GetExponentialHistogram().GetDataPoints()
	require.Len(t, points, 1)
	require.Equal(t, uint64(3), points[0].GetCount())
	require.Equal(t, int32(0), points[0].GetScale())
	require.Equal(t, int32(-1), points[0].GetPositive().GetOffset())
	require.Equal(t, []uint64{1, 1, 1}, points[0].GetPositive().GetBucketCounts())
	explicit := received()["explicit_ms"]
	require.Nil(t, explicit.GetExponentialHistogram())
	require.Len(t, explicit.GetHistogram().GetDataPoints(), 1)
	require.Equal(t, []float64{100, 200}, explicit.GetHistogram().GetDataPoints()[0].GetExplicitBounds())
}
