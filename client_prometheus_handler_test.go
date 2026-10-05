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

	"github.com/convoy-road-trips-app/stats/exporters/prometheus"
)

func scrape(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(h)
	defer srv.Close()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}

func TestScrapeCounter(t *testing.T) {
	h := &prometheus.Handler{}
	client, err := NewClient(WithFlushInterval(time.Hour), WithPrometheusHandler(h))
	require.NoError(t, err)
	defer client.Close()

	ctx := context.Background()
	for range 3 {
		require.NoError(t, client.Counter(ctx, "http.requests", 1, WithAttribute("method", "GET")))
	}
	require.NoError(t, client.Flush(ctx))

	body := scrape(t, h)
	require.Contains(t, body, "# TYPE http_requests_total counter\n")
	require.Contains(t, body, `http_requests_total{method="GET"} 3`+"\n")
	_, ok := client.Stats().Pipeline.ExporterErrors[prometheus.PullExporterName]
	require.True(t, ok, "handler gets an ExporterErrors slot")
}

func TestScrapeHistogramBuckets(t *testing.T) {
	h := &prometheus.Handler{}
	client, err := NewClient(WithFlushInterval(time.Hour), WithPrometheusHandler(h))
	require.NoError(t, err)
	defer client.Close()

	ctx := context.Background()
	require.NoError(t, client.Histogram(ctx, "db.query", 0.2))
	require.NoError(t, client.Flush(ctx))

	body := scrape(t, h)
	require.Contains(t, body, "# TYPE db_query histogram\n")
	require.Contains(t, body, `db_query_bucket{le="+Inf"} 1`+"\n")
	require.Contains(t, body, "db_query_count 1\n")
}

func TestScrapePerMetricBuckets(t *testing.T) {
	h := &prometheus.Handler{}
	// The bucket option comes after the handler: order must not matter.
	client, err := NewClient(
		WithFlushInterval(time.Hour),
		WithPrometheusHandler(h),
		WithHistogramBucketsFor("size", 10, 100),
	)
	require.NoError(t, err)
	defer client.Close()

	ctx := context.Background()
	require.NoError(t, client.Histogram(ctx, "size", 50))
	require.NoError(t, client.Histogram(ctx, "other", 50))
	require.NoError(t, client.Flush(ctx))

	body := scrape(t, h)
	require.Contains(t, body, `size_bucket{le="10"} 0`+"\n")
	require.Contains(t, body, `size_bucket{le="100"} 1`+"\n")
	require.Contains(t, body, `size_bucket{le="+Inf"} 1`+"\n")
	require.Contains(t, body, `other_bucket{le="0.005"} 0`+"\n") // default bounds
	require.Contains(t, body, `other_bucket{le="+Inf"} 1`+"\n")
}

func TestPrometheusHandlerKeepsUserBuckets(t *testing.T) {
	h := &prometheus.Handler{Buckets: func(string) []float64 { return []float64{1, 2} }}
	client, err := NewClient(
		WithFlushInterval(time.Hour),
		WithPrometheusHandler(h),
		WithHistogramBucketsFor("size", 10, 100),
	)
	require.NoError(t, err)
	defer client.Close()

	ctx := context.Background()
	require.NoError(t, client.Histogram(ctx, "size", 1.5, WithAttribute("k", "v")))
	require.NoError(t, client.Flush(ctx))

	body := scrape(t, h)
	require.Contains(t, body, `size_bucket{k="v",le="2"} 1`+"\n")
	require.NotContains(t, body, `le="10"`)
}

func TestPrometheusHandlerNilIsInvalid(t *testing.T) {
	_, err := NewClient(WithPrometheusHandler(nil))
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidConfig)
}

func TestPrometheusHandlerMethodNotAllowedViaClient(t *testing.T) {
	h := &prometheus.Handler{}
	client, err := NewClient(WithFlushInterval(time.Hour), WithPrometheusHandler(h))
	require.NoError(t, err)
	defer client.Close()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/metrics", strings.NewReader(""))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}
