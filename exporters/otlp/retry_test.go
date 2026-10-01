package otlp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/convoy-road-trips-app/stats/models"
	"github.com/stretchr/testify/require"
	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// flakyReceiver is a real OTLP/HTTP receiver that answers 503 to the first
// `failures` requests (all requests when failures < 0) and records the rest.
type flakyReceiver struct {
	server   *httptest.Server
	attempts atomic.Int64
	received chan *collectormetricspb.ExportMetricsServiceRequest
}

func newFlakyReceiver(t *testing.T, failures int64) *flakyReceiver {
	t.Helper()
	r := &flakyReceiver{received: make(chan *collectormetricspb.ExportMetricsServiceRequest, 16)}
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
		r.received <- &request
	}))
	t.Cleanup(r.server.Close)
	return r
}

func (r *flakyReceiver) exporter(t *testing.T, retry *models.OTLPRetry) *Exporter {
	t.Helper()
	exporter, err := NewExporter(&models.OTLPConfig{
		Enabled: true, Endpoint: strings.TrimPrefix(r.server.URL, "http://"), Insecure: true,
		Protocol: models.OTLPProtocolHTTP, Retry: retry,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = exporter.Shutdown(context.Background()) })
	return exporter
}

func counterBatch(value float64) []*models.Metric {
	return []*models.Metric{{Name: "requests_total", Type: models.MetricTypeCounter, Value: value, Timestamp: time.Now()}}
}

func TestExporter_OTLPHTTP_retry_resends_without_duplicating_cumulative_data(t *testing.T) {
	// Given: a receiver that rejects the first request with a retryable 503
	receiver := newFlakyReceiver(t, 1)
	exporter := receiver.exporter(t, &models.OTLPRetry{
		InitialInterval: time.Millisecond, MaxInterval: 5 * time.Millisecond, MaxElapsedTime: time.Second,
	})

	// When
	require.NoError(t, exporter.Export(context.Background(), counterBatch(2)))
	require.NoError(t, exporter.Export(context.Background(), counterBatch(3)))

	// Then: the retried payload carries the value once, and the next export continues from it
	require.Equal(t, int64(3), receiver.attempts.Load())
	first, second := <-receiver.received, <-receiver.received
	require.InDelta(t, float64(2), wireMetric(t, first, "requests_total").GetSum().DataPoints[0].GetAsDouble(), 0.001)
	require.InDelta(t, float64(5), wireMetric(t, second, "requests_total").GetSum().DataPoints[0].GetAsDouble(), 0.001)
}

func TestExporter_OTLPHTTP_retry_stops_after_max_elapsed_time(t *testing.T) {
	// Given: a receiver that is always overloaded and a short elapsed-time budget
	receiver := newFlakyReceiver(t, -1)
	exporter := receiver.exporter(t, &models.OTLPRetry{
		InitialInterval: time.Millisecond, MaxInterval: 2 * time.Millisecond, MaxElapsedTime: 50 * time.Millisecond,
	})
	done := make(chan error, 1)

	// When
	go func() { done <- exporter.Export(context.Background(), counterBatch(1)) }()

	// Then: it retried, gave up on its own, and did not report a context error
	select {
	case err := <-done:
		require.Error(t, err)
		require.False(t, errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled), "err = %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Export kept retrying past MaxElapsedTime")
	}
	require.Greater(t, receiver.attempts.Load(), int64(1))
}

func TestExporter_OTLPHTTP_retry_stops_when_caller_context_expires(t *testing.T) {
	// Given: a retry policy far longer than the caller's deadline
	receiver := newFlakyReceiver(t, -1)
	exporter := receiver.exporter(t, &models.OTLPRetry{
		InitialInterval: time.Hour, MaxInterval: time.Hour, MaxElapsedTime: 2 * time.Hour,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)

	// When
	go func() { done <- exporter.Export(ctx, counterBatch(1)) }()

	// Then
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(5 * time.Second):
		t.Fatal("Export ignored the caller deadline while waiting to retry")
	}
}

func TestOTLPConfig_Validate_rejects_invalid_retry(t *testing.T) {
	cases := map[string]models.OTLPRetry{
		"zero initial interval":      {InitialInterval: 0, MaxInterval: time.Second, MaxElapsedTime: time.Second},
		"max interval below initial": {InitialInterval: time.Second, MaxInterval: time.Millisecond, MaxElapsedTime: time.Second},
		"unbounded elapsed time":     {InitialInterval: time.Millisecond, MaxInterval: time.Second, MaxElapsedTime: 0},
		"negative elapsed time":      {InitialInterval: time.Millisecond, MaxInterval: time.Second, MaxElapsedTime: -time.Second},
	}
	for name, retry := range cases {
		t.Run(name, func(t *testing.T) {
			// Given
			config := &models.OTLPConfig{Enabled: true, Endpoint: "localhost:4318", Retry: &retry}

			// When
			err := config.Validate()

			// Then
			require.Error(t, err)
		})
	}
}
