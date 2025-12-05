// +build integration

package integration

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/convoy-road-trips-app/stats"
)

// TestDatadogIntegration tests sending metrics to Datadog agent
func TestDatadogIntegration(t *testing.T) {
	// Create client with Datadog backend
	client, err := stats.NewClient(
		stats.WithServiceName("integration-test"),
		stats.WithEnvironment("test"),
		stats.WithBufferSize(1024),
		stats.WithWorkers(2),
		stats.WithDatadog(&stats.DatadogConfig{
			AgentHost: "localhost",
			AgentPort: 8125,
			Tags:      []string{"test:integration", "backend:datadog"},
		}),
	)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	ctx := context.Background()

	// Send test metrics
	t.Run("Counter", func(t *testing.T) {
		for i := 0; i < 100; i++ {
			err := client.Counter(ctx, "test.counter", 1.0,
				stats.WithAttribute("iteration", fmt.Sprintf("%d", i)),
			)
			if err != nil {
				t.Errorf("Failed to send counter: %v", err)
			}
		}
	})

	t.Run("Gauge", func(t *testing.T) {
		for i := 0; i < 50; i++ {
			err := client.Gauge(ctx, "test.gauge", float64(i)*1.5,
				stats.WithAttribute("value", fmt.Sprintf("%.1f", float64(i)*1.5)),
			)
			if err != nil {
				t.Errorf("Failed to send gauge: %v", err)
			}
		}
	})

	t.Run("Histogram", func(t *testing.T) {
		for i := 0; i < 75; i++ {
			err := client.Histogram(ctx, "test.histogram", float64(i)*2.0,
				stats.WithAttribute("bucket", fmt.Sprintf("%d", i/10)),
			)
			if err != nil {
				t.Errorf("Failed to send histogram: %v", err)
			}
		}
	})

	// Wait for metrics to be processed
	time.Sleep(500 * time.Millisecond)

	// Verify metrics were sent
	clientStats := client.Stats()
	t.Logf("Datadog Stats: Processed=%d, Dropped=%d, Errors=%d",
		clientStats.Pipeline.Processed, clientStats.Pipeline.Dropped, clientStats.Pipeline.Errors)

	if clientStats.Pipeline.Processed == 0 {
		t.Error("No metrics were processed")
	}

	if clientStats.Pipeline.Errors > 0 {
		t.Errorf("Unexpected errors: %d", clientStats.Pipeline.Errors)
	}
}

// TestPrometheusIntegration tests sending metrics to Prometheus via StatsD exporter
func TestPrometheusIntegration(t *testing.T) {
	// Create client with Prometheus backend
	client, err := stats.NewClient(
		stats.WithServiceName("integration-test"),
		stats.WithEnvironment("test"),
		stats.WithBufferSize(1024),
		stats.WithWorkers(2),
		stats.WithPrometheus(&stats.PrometheusConfig{
			PushgatewayAddress: "localhost:9125",
			Job:                "integration-test",
			Instance:           "test-instance",
		}),
	)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	ctx := context.Background()

	// Send test metrics
	t.Run("SendMetrics", func(t *testing.T) {
		// Counters
		for i := 0; i < 50; i++ {
			err := client.Counter(ctx, "prom.test.requests", 1.0,
				stats.WithAttribute("method", "GET"),
				stats.WithAttribute("status", "200"),
			)
			if err != nil {
				t.Errorf("Failed to send counter: %v", err)
			}
		}

		// Gauges
		for i := 0; i < 25; i++ {
			err := client.Gauge(ctx, "prom.test.connections", float64(i),
				stats.WithAttribute("server", "web-1"),
			)
			if err != nil {
				t.Errorf("Failed to send gauge: %v", err)
			}
		}
	})

	// Wait for metrics to be processed and exported
	time.Sleep(2 * time.Second)

	// Query StatsD exporter metrics endpoint
	t.Run("VerifyMetrics", func(t *testing.T) {
		resp, err := http.Get("http://localhost:9102/metrics")
		if err != nil {
			t.Fatalf("Failed to query metrics endpoint: %v", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("Failed to read response: %v", err)
		}

		metrics := string(body)
		t.Logf("Received %d bytes of metrics", len(metrics))

		// Check if our metrics appear in the output
		if !strings.Contains(metrics, "prom_test") {
			t.Error("Test metrics not found in Prometheus output")
		}
	})

	// Verify client stats
	clientStats := client.Stats()
	t.Logf("Prometheus Stats: Processed=%d, Dropped=%d, Errors=%d",
		clientStats.Pipeline.Processed, clientStats.Pipeline.Dropped, clientStats.Pipeline.Errors)

	if clientStats.Pipeline.Processed == 0 {
		t.Error("No metrics were processed")
	}
}

// TestMultiBackendIntegration tests sending to multiple backends simultaneously
func TestMultiBackendIntegration(t *testing.T) {
	// Create client with both backends
	client, err := stats.NewClient(
		stats.WithServiceName("multi-backend-test"),
		stats.WithEnvironment("test"),
		stats.WithBufferSize(2048),
		stats.WithWorkers(4),
		stats.WithDatadog(&stats.DatadogConfig{
			AgentHost: "localhost",
			AgentPort: 8125,
			Tags:      []string{"test:multi"},
		}),
		stats.WithPrometheus(&stats.PrometheusConfig{
			PushgatewayAddress: "localhost:9125",
			Job:                "multi-backend-test",
			Instance:           "test-instance",
		}),
	)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	ctx := context.Background()

	// Send metrics concurrently
	t.Run("ConcurrentSend", func(t *testing.T) {
		done := make(chan bool, 10)

		for goroutine := 0; goroutine < 10; goroutine++ {
			go func(id int) {
				defer func() { done <- true }()

				for i := 0; i < 20; i++ {
					_ = client.Counter(ctx, "multi.concurrent.test", 1.0,
						stats.WithAttribute("goroutine", fmt.Sprintf("%d", id)),
						stats.WithAttribute("iteration", fmt.Sprintf("%d", i)),
					)
				}
			}(goroutine)
		}

		// Wait for all goroutines
		for i := 0; i < 10; i++ {
			<-done
		}
	})

	// Wait for processing
	time.Sleep(1 * time.Second)

	// Verify stats
	clientStats := client.Stats()
	t.Logf("Multi-Backend Stats: Processed=%d, Dropped=%d, Errors=%d",
		clientStats.Pipeline.Processed, clientStats.Pipeline.Dropped, clientStats.Pipeline.Errors)

	t.Logf("Per-Exporter Errors: %v", clientStats.Pipeline.ExporterErrors)

	if clientStats.Pipeline.Processed < 150 {
		t.Errorf("Expected at least 150 processed metrics, got %d", clientStats.Pipeline.Processed)
	}
}

// TestRateLimitingIntegration tests rate limiting functionality
func TestRateLimitingIntegration(t *testing.T) {
	// Create client with strict rate limiting
	client, err := stats.NewClient(
		stats.WithServiceName("ratelimit-test"),
		stats.WithEnvironment("test"),
		stats.WithBufferSize(1024),
		stats.WithWorkers(2),
		stats.WithRateLimit(100, 50), // 100 metrics/sec, burst of 50
		stats.WithDatadog(&stats.DatadogConfig{
			AgentHost: "localhost",
			AgentPort: 8125,
		}),
	)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	ctx := context.Background()

	// Try to send more metrics than allowed
	t.Run("ExceedRateLimit", func(t *testing.T) {
		rateLimitedCount := 0

		// Try to send 500 metrics quickly (should exceed rate limit)
		for i := 0; i < 500; i++ {
			err := client.Counter(ctx, "ratelimit.test", 1.0)
			if err == stats.ErrRateLimitExceeded {
				rateLimitedCount++
			}
		}

		t.Logf("Rate limited: %d metrics", rateLimitedCount)

		if rateLimitedCount == 0 {
			t.Error("Expected some metrics to be rate limited")
		}
	})

	// Wait for processing
	time.Sleep(500 * time.Millisecond)

	// Check stats
	clientStats := client.Stats()
	t.Logf("Rate Limiting Stats: Processed=%d, RateLimited=%d",
		clientStats.Pipeline.Processed, clientStats.Pipeline.RateLimited)

	if clientStats.Pipeline.RateLimiter != nil {
		t.Logf("Rate Limiter: Rate=%.0f/sec, Burst=%d, Utilization=%.2f%%",
			clientStats.Pipeline.RateLimiter.Rate,
			clientStats.Pipeline.RateLimiter.Burst,
			clientStats.Pipeline.RateLimiter.Utilization*100)
	}

	if clientStats.Pipeline.RateLimited == 0 {
		t.Error("Expected some metrics to be rate limited, but got 0")
	}

	// Verify rate limiter is working
	if clientStats.Pipeline.RateLimited < 300 {
		t.Errorf("Expected at least 300 rate limited metrics, got %d", clientStats.Pipeline.RateLimited)
	}
}
