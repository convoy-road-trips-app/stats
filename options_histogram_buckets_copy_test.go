package stats

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

func TestNewClient_OTLP_exports_configured_bounds_when_caller_mutates_bucket_slice_after_construction(t *testing.T) {
	// Given: an OTLP/HTTP receiver and a client built from a caller-owned bucket slice
	received := make(chan []float64, 16)
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
		for _, rm := range request.GetResourceMetrics() {
			for _, sm := range rm.GetScopeMetrics() {
				for _, m := range sm.GetMetrics() {
					if m.GetName() == "checkout_latency_seconds" {
						received <- m.GetHistogram().GetDataPoints()[0].GetExplicitBounds()
					}
				}
			}
		}
	}))
	defer server.Close()
	bounds := []float64{1, 2, 3}
	client, err := NewClient(WithFlushInterval(time.Hour), WithOTLP(&OTLPConfig{
		Endpoint:         strings.TrimPrefix(server.URL, "http://"),
		Insecure:         true,
		Protocol:         OTLPProtocolHTTP,
		HistogramBuckets: bounds,
	}))
	require.NoError(t, err)
	defer func() { require.NoError(t, client.Shutdown(context.Background())) }()

	// When: the caller reuses its slice after the client was built
	bounds[0], bounds[1], bounds[2] = 10, 20, 30
	require.NoError(t, client.Histogram(context.Background(), "checkout_latency_seconds", 1.5))
	require.NoError(t, client.Flush(context.Background()))

	// Then
	select {
	case got := <-received:
		require.Equal(t, []float64{1, 2, 3}, got)
	case <-time.After(5 * time.Second):
		t.Fatal("OTLP receiver got no histogram within 5s")
	}
}

func TestWithOTLP_reused_option_gives_each_config_its_own_bucket_slice(t *testing.T) {
	// Given: one option applied to a first config whose buckets are then mutated
	option := WithOTLP(&OTLPConfig{HistogramBuckets: []float64{1, 2, 3}})
	first, second := DefaultConfig(), DefaultConfig()
	option(first)
	first.OTLP.HistogramBuckets[0] = 0.5

	// When
	option(second)

	// Then
	require.Equal(t, []float64{1, 2, 3}, second.OTLP.HistogramBuckets)
}

func TestWithOTLP_without_buckets_keeps_default_buckets_unset(t *testing.T) {
	// Given
	config := DefaultConfig()

	// When
	WithOTLP(&OTLPConfig{Endpoint: "localhost:4317"})(config)

	// Then: nil means the exporter falls back to the D9 seconds buckets
	require.Nil(t, config.OTLP.HistogramBuckets)
}

func TestWithOTLP_without_buckets_keeps_earlier_WithHistogramBuckets(t *testing.T) {
	// Given
	config := DefaultConfig()
	WithHistogramBuckets([]float64{0.1, 1})(config)

	// When
	WithOTLP(&OTLPConfig{Endpoint: "localhost:4317"})(config)

	// Then
	require.Equal(t, []float64{0.1, 1}, config.OTLP.HistogramBuckets)
}

func TestWithOTLP_empty_buckets_still_fail_validation(t *testing.T) {
	// Given
	config := DefaultConfig()

	// When
	WithOTLP(&OTLPConfig{Endpoint: "localhost:4317", HistogramBuckets: []float64{}})(config)

	// Then: an explicit empty slice is not silently turned into the defaults
	require.Error(t, ValidateConfig(config))
}
