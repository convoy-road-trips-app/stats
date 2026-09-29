package stats

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// otlpReceiver is a real OTLP/HTTP receiver on an ephemeral port. It answers
// 503 to the first `failures` requests (every request when failures < 0) and
// keeps the largest cumulative sum it has received per metric name.
type otlpReceiver struct {
	server   *httptest.Server
	attempts atomic.Int64
	mu       sync.Mutex
	sums     map[string]float64
}

func newOTLPReceiver(t *testing.T, failures int64) *otlpReceiver {
	t.Helper()
	r := &otlpReceiver{sums: make(map[string]float64)}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		attempt := r.attempts.Add(1)
		body, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if failures < 0 || attempt <= failures {
			http.Error(w, "collector overloaded", http.StatusServiceUnavailable)
			return
		}
		var request collectormetricspb.ExportMetricsServiceRequest
		if err := proto.Unmarshal(body, &request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		r.record(&request)
	}))
	t.Cleanup(r.server.Close)
	return r
}

func (r *otlpReceiver) record(request *collectormetricspb.ExportMetricsServiceRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, resource := range request.GetResourceMetrics() {
		for _, scope := range resource.GetScopeMetrics() {
			for _, metric := range scope.GetMetrics() {
				for _, point := range metric.GetSum().GetDataPoints() {
					r.sums[metric.GetName()] = max(r.sums[metric.GetName()], point.GetAsDouble())
				}
			}
		}
	}
}

func (r *otlpReceiver) sum(name string) float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sums[name]
}

func (r *otlpReceiver) option() Option {
	return WithOTLP(&OTLPConfig{
		Endpoint: strings.TrimPrefix(r.server.URL, "http://"),
		Insecure: true,
		Protocol: OTLPProtocolHTTP,
	})
}

func TestClient_Flush_delivers_observations_and_drop_counter_to_OTLP_receiver_before_returning(t *testing.T) {
	// Given: only an explicit Flush can export (no full batch, no ticker)
	receiver := newOTLPReceiver(t, 0)
	client, err := NewClient(WithServiceName("flush-e2e"), WithFlushInterval(time.Hour), WithMaxCardinality(1), receiver.option())
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Shutdown(context.Background()) })
	ctx := context.Background()
	for range 3 {
		require.NoError(t, client.Counter(ctx, "requests_total", 1))
	}
	require.NoError(t, client.Counter(ctx, "jobs_total", 1, WithAttribute("job_id", "0")))
	require.ErrorIs(t, client.Counter(ctx, "jobs_total", 1, WithAttribute("job_id", "1")), ErrCardinalityLimit)

	// When
	err = within(t, func() error { return client.Flush(ctx) })

	// Then: the receiver already holds everything when Flush returns
	require.NoError(t, err)
	require.Equal(t, float64(3), receiver.sum("requests_total"))
	require.Equal(t, float64(1), receiver.sum("jobs_total"))
	require.Equal(t, float64(1), receiver.sum(droppedLabelsMetric))
}

func TestClient_Shutdown_delivers_10k_accepted_counter_increments_to_OTLP_receiver(t *testing.T) {
	// Given
	receiver := newOTLPReceiver(t, 0)
	client, err := NewClient(WithServiceName("drain-e2e"), receiver.option())
	require.NoError(t, err)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 1250 {
				require.NoError(t, client.Counter(context.Background(), "requests_total", 1))
			}
		})
	}
	wg.Wait()

	// When
	err = within(t, func() error { return client.Shutdown(context.Background()) })

	// Then
	require.NoError(t, err)
	require.Equal(t, float64(10_000), receiver.sum("requests_total"))
}

func TestClient_Flush_with_OTLP_retry_recovers_from_transient_unavailability(t *testing.T) {
	// Given: the first export attempt gets a retryable 503
	receiver := newOTLPReceiver(t, 1)
	client, err := NewClient(WithServiceName("retry-e2e"), WithFlushInterval(time.Hour),
		WithOTLPRetry(time.Millisecond, 5*time.Millisecond, time.Second), receiver.option())
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Shutdown(context.Background()) })
	require.NoError(t, client.Counter(context.Background(), "requests_total", 2))

	// When
	err = within(t, func() error { return client.Flush(context.Background()) })

	// Then
	require.NoError(t, err)
	require.Equal(t, int64(2), receiver.attempts.Load())
	require.Equal(t, float64(2), receiver.sum("requests_total"))
}

func TestClient_Flush_returns_deadline_exceeded_while_OTLP_retry_waits(t *testing.T) {
	// Given: a collector that never recovers and a retry backoff longer than the deadline
	receiver := newOTLPReceiver(t, -1)
	client, err := NewClient(WithServiceName("retry-deadline"), WithFlushInterval(time.Hour),
		receiver.option(), WithOTLPRetry(time.Hour, time.Hour, 2*time.Hour))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Shutdown(context.Background()) })
	require.NoError(t, client.Counter(context.Background(), "requests_total", 1))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// When
	err = within(t, func() error { return client.Flush(ctx) })

	// Then
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestClient_Flush_after_Shutdown_returns_client_closed(t *testing.T) {
	// Given
	client, err := NewClient(WithServiceName("closed"))
	require.NoError(t, err)
	require.NoError(t, client.Shutdown(context.Background()))

	// When
	err = client.Flush(context.Background())

	// Then
	require.ErrorIs(t, err, ErrClientClosed)
}

func TestWithOTLPRetry_rejects_unbounded_elapsed_time(t *testing.T) {
	// Given
	cfg := DefaultConfig()
	WithOTLPRetry(time.Millisecond, time.Second, 0)(cfg)

	// When
	err := ValidateConfig(cfg)

	// Then
	require.Error(t, err)
}
