//go:build integration

package lgtm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/convoy-road-trips-app/stats"
	statsOtel "github.com/convoy-road-trips-app/stats/otel"
)

const (
	prometheusURL = "http://localhost:9090"
	otlpEndpoint  = "localhost:4317"
	queryTimeout  = 60 * time.Second
	pollInterval  = 2 * time.Second
)

func TestMain(m *testing.M) {
	if err := waitForReady(); err != nil {
		fmt.Fprintf(os.Stderr, "LGTM stack not ready: %v\n", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func waitForReady() error {
	deadline := time.Now().Add(90 * time.Second)
	promReady := false
	otlpReady := false
	httpClient := &http.Client{Timeout: 5 * time.Second}

	for time.Now().Before(deadline) {
		if !promReady {
			resp, err := httpClient.Get(prometheusURL + "/-/ready")
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					promReady = true
				}
			}
		}

		if !otlpReady {
			conn, err := net.DialTimeout("tcp", otlpEndpoint, 2*time.Second)
			if err == nil {
				conn.Close()
				otlpReady = true
			}
		}

		if promReady && otlpReady {
			time.Sleep(5 * time.Second)
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("LGTM stack not ready after 90s (prometheus=%v, otlp=%v)", promReady, otlpReady)
}

type promResponse struct {
	Status string   `json:"status"`
	Data   promData `json:"data"`
}

type promData struct {
	ResultType string       `json:"resultType"`
	Result     []promResult `json:"result"`
}

type promResult struct {
	Metric map[string]string `json:"metric"`
	Value  []json.RawMessage `json:"value"`
}

func queryProm(t *testing.T, promQL string) *promResponse {
	t.Helper()
	u := fmt.Sprintf("%s/api/v1/query?query=%s", prometheusURL, url.QueryEscape(promQL))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var result promResponse
	require.NoError(t, json.Unmarshal(body, &result))
	return &result
}

func waitForMetric(t *testing.T, promQL string) *promResponse {
	t.Helper()
	deadline := time.Now().Add(queryTimeout)
	var last *promResponse

	for time.Now().Before(deadline) {
		last = queryProm(t, promQL)
		if last.Status == "success" && len(last.Data.Result) > 0 {
			return last
		}
		time.Sleep(pollInterval)
	}

	t.Fatalf("metric not found within %v: query=%s", queryTimeout, promQL)
	return nil
}

func newOTLPClient(t *testing.T, svc string) *stats.Client {
	t.Helper()
	client, err := stats.NewClient(
		stats.WithServiceName(svc),
		stats.WithEnvironment("test"),
		stats.WithBufferSize(1024),
		stats.WithWorkers(2),
		stats.WithOTLP(&stats.OTLPConfig{
			Endpoint:    otlpEndpoint,
			Insecure:    true,
			ServiceName: svc,
		}),
	)
	require.NoError(t, err)
	return client
}

func TestLGTM_LegacyMode(t *testing.T) {
	client := newOTLPClient(t, "lgtm-legacy")
	ctx := context.Background()

	for i := range 10 {
		require.NoError(t, client.Counter(ctx, "lgtm_legacy_requests", 1.0,
			stats.WithAttribute("method", "GET"),
			stats.WithAttribute("iter", fmt.Sprintf("%d", i)),
		))
	}

	require.NoError(t, client.Gauge(ctx, "lgtm_legacy_memory", 72.5,
		stats.WithAttribute("unit", "percent"),
	))

	for i := range 5 {
		require.NoError(t, client.Histogram(ctx, "lgtm_legacy_latency", float64(i+1)*25.0,
			stats.WithAttribute("endpoint", "/api"),
		))
	}

	require.NoError(t, client.Close())

	t.Run("VerifyCounter", func(t *testing.T) {
		resp := waitForMetric(t, `{__name__=~"lgtm_legacy_requests.*"}`)
		assert.NotEmpty(t, resp.Data.Result)
	})

	t.Run("VerifyGauge", func(t *testing.T) {
		resp := waitForMetric(t, `{__name__=~"lgtm_legacy_memory.*"}`)
		assert.NotEmpty(t, resp.Data.Result)
	})

	t.Run("VerifyHistogram", func(t *testing.T) {
		resp := waitForMetric(t, `{__name__=~"lgtm_legacy_latency.*"}`)
		assert.NotEmpty(t, resp.Data.Result)
	})
}

func TestLGTM_OTelMode(t *testing.T) {
	provider, err := statsOtel.NewMeterProvider(
		statsOtel.WithStatsOptions(
			stats.WithServiceName("lgtm-otel"),
			stats.WithEnvironment("test"),
			stats.WithBufferSize(1024),
			stats.WithWorkers(2),
			stats.WithOTLP(&stats.OTLPConfig{
				Endpoint:    otlpEndpoint,
				Insecure:    true,
				ServiceName: "lgtm-otel",
			}),
		),
	)
	require.NoError(t, err)

	meter := provider.Meter("lgtm-test")
	ctx := context.Background()

	counter, err := meter.Int64Counter("lgtm_otel_events")
	require.NoError(t, err)
	for range 10 {
		counter.Add(ctx, 1, metric.WithAttributes(
			attribute.String("source", "test"),
		))
	}

	hist, err := meter.Float64Histogram("lgtm_otel_duration")
	require.NoError(t, err)
	for i := range 5 {
		hist.Record(ctx, float64(i+1)*33.3, metric.WithAttributes(
			attribute.String("op", "query"),
		))
	}

	gauge, err := meter.Float64Gauge("lgtm_otel_cpu")
	require.NoError(t, err)
	gauge.Record(ctx, 65.2, metric.WithAttributes(
		attribute.String("host", "web-1"),
	))

	require.NoError(t, provider.Shutdown(ctx))

	t.Run("VerifyCounter", func(t *testing.T) {
		resp := waitForMetric(t, `{__name__=~"lgtm_otel_events.*"}`)
		assert.NotEmpty(t, resp.Data.Result)
	})

	t.Run("VerifyHistogram", func(t *testing.T) {
		resp := waitForMetric(t, `{__name__=~"lgtm_otel_duration.*"}`)
		assert.NotEmpty(t, resp.Data.Result)
	})

	t.Run("VerifyGauge", func(t *testing.T) {
		resp := waitForMetric(t, `{__name__=~"lgtm_otel_cpu.*"}`)
		assert.NotEmpty(t, resp.Data.Result)
	})
}

func TestLGTM_ConcurrentExport(t *testing.T) {
	client := newOTLPClient(t, "lgtm-concurrent")
	ctx := context.Background()

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		errors []error
	)
	for g := range 8 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := range 25 {
				if err := client.Counter(ctx, "lgtm_concurrent_ops", 1.0,
					stats.WithAttribute("goroutine", fmt.Sprintf("%d", id)),
					stats.WithAttribute("iter", fmt.Sprintf("%d", i)),
				); err != nil {
					mu.Lock()
					errors = append(errors, err)
					mu.Unlock()
				}
			}
		}(g)
	}
	wg.Wait()
	require.Empty(t, errors, "concurrent Counter calls returned errors: %v", errors)
	require.NoError(t, client.Close())

	resp := waitForMetric(t, `{__name__=~"lgtm_concurrent_ops.*"}`)
	assert.NotEmpty(t, resp.Data.Result)
}

func TestLGTM_Attributes(t *testing.T) {
	client := newOTLPClient(t, "lgtm-attrs")
	ctx := context.Background()

	require.NoError(t, client.Counter(ctx, "lgtm_attr_check", 1.0,
		stats.WithAttribute("env", "test"),
		stats.WithAttribute("region", "us-east-1"),
		stats.WithAttribute("version", "v1.2.3"),
	))

	require.NoError(t, client.Close())

	resp := waitForMetric(t, `{__name__=~"lgtm_attr_check.*", env="test"}`)
	require.NotEmpty(t, resp.Data.Result)

	found := resp.Data.Result[0].Metric
	assert.Equal(t, "test", found["env"])
	assert.Equal(t, "us-east-1", found["region"])
	assert.Equal(t, "v1.2.3", found["version"])
}
