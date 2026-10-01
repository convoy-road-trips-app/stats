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

// seriesSnapshot is what one export request holds for a series.
type seriesSnapshot struct {
	value float64 // counter sum, histogram sum or gauge value
	start uint64
}

// snapshotReceiver records, per OTLP/HTTP export request, every series it holds.
type snapshotReceiver struct {
	server  *httptest.Server
	mu      sync.Mutex
	exports []map[string]seriesSnapshot
}

func newSnapshotReceiver(t *testing.T) *snapshotReceiver {
	t.Helper()
	r := &snapshotReceiver{}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var request collectormetricspb.ExportMetricsServiceRequest
		if err := proto.Unmarshal(body, &request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		snapshot := map[string]seriesSnapshot{}
		for _, resource := range request.GetResourceMetrics() {
			for _, scope := range resource.GetScopeMetrics() {
				for _, metric := range scope.GetMetrics() {
					for _, p := range metric.GetSum().GetDataPoints() {
						snapshot[metric.GetName()] = seriesSnapshot{p.GetAsDouble(), p.GetStartTimeUnixNano()}
					}
					for _, p := range metric.GetGauge().GetDataPoints() {
						snapshot[metric.GetName()] = seriesSnapshot{value: p.GetAsDouble()}
					}
					for _, p := range metric.GetHistogram().GetDataPoints() {
						snapshot[metric.GetName()] = seriesSnapshot{p.GetSum(), p.GetStartTimeUnixNano()}
					}
				}
			}
		}
		r.mu.Lock()
		r.exports = append(r.exports, snapshot)
		r.mu.Unlock()
	}))
	t.Cleanup(r.server.Close)
	return r
}

// complete returns the exports that hold all of names.
func (r *snapshotReceiver) complete(names ...string) []map[string]seriesSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []map[string]seriesSnapshot
	for _, export := range r.exports {
		all := true
		for _, name := range names {
			if _, ok := export[name]; !ok {
				all = false
			}
		}
		if all {
			out = append(out, export)
		}
	}
	return out
}

func TestClient_OTLPcumulative_reexports_every_series_in_intervals_without_observations(t *testing.T) {
	// Given: a counter, a histogram and a gauge recorded once
	receiver := newSnapshotReceiver(t)
	client, err := NewClient(WithServiceName("reexport"), WithFlushInterval(20*time.Millisecond),
		WithOTLP(&OTLPConfig{
			Endpoint: strings.TrimPrefix(receiver.server.URL, "http://"),
			Insecure: true,
			Protocol: OTLPProtocolHTTP,
		}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Shutdown(context.Background()) })
	ctx := context.Background()
	require.NoError(t, client.Counter(ctx, "sync_total", 3))
	require.NoError(t, client.Histogram(ctx, "sync_seconds", 0.5))
	require.NoError(t, client.Gauge(ctx, "sync_last", 7))
	names := []string{"sync_total", "sync_seconds", "sync_last"}

	// When: further export intervals pass with no new observations
	require.Eventually(t, func() bool { return len(receiver.complete(names...)) >= 3 },
		5*time.Second, 10*time.Millisecond)

	// Then: every export holds all three series with the same values and start time
	exports := receiver.complete(names...)
	for _, export := range exports[1:] {
		require.Equal(t, exports[0], export)
	}
	require.InDelta(t, 3, exports[0]["sync_total"].value, 0.001)
	require.InDelta(t, 0.5, exports[0]["sync_seconds"].value, 0.001)
	require.InDelta(t, 7, exports[0]["sync_last"].value, 0.001)
}
