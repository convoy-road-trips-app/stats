package main

import (
	"fmt"
	"time"

	"github.com/convoy-road-trips-app/stats"
)

func main() {
	fmt.Println("=== Multi-Backend Stats Example ===")
	fmt.Println()

	// Create a client with all three backends enabled
	client, err := stats.NewClient(
		stats.WithServiceName("multibackend-demo"),
		stats.WithEnvironment("development"),
		stats.WithBufferSize(1024),
		stats.WithWorkers(2),

		// Enable Datadog
		stats.WithDatadog(&stats.DatadogConfig{
			AgentHost: "localhost",
			AgentPort: 8125,
			Tags:      []string{"env:dev", "app:demo"},
		}),

		// Enable Prometheus
		stats.WithPrometheus(&stats.PrometheusConfig{
			PushgatewayAddress: "localhost:9091",
			Job:                "stats_demo",
			Instance:           "instance1",
		}),

		// Enable CloudWatch
		stats.WithCloudWatch(&stats.CloudWatchConfig{
			AgentHost: "localhost",
			AgentPort: 25888,
			Namespace: "StatsDemo",
			Region:    "us-east-1",
		}),
	)
	if err != nil {
		panic(fmt.Sprintf("Failed to create client: %v", err))
	}
	defer client.Close()

	fmt.Println("✅ Stats client created with 3 backends:")
	fmt.Println("  - Datadog (localhost:8125)")
	fmt.Println("  - Prometheus (localhost:9091)")
	fmt.Println("  - CloudWatch (localhost:25888)")
	fmt.Println()

	// Record various types of metrics
	fmt.Println("Recording metrics...")

	// Counter - requests per second
	for i := 0; i < 10; i++ {
		client.Counter("http.requests", 1.0,
			stats.WithAttribute("method", "GET"),
			stats.WithAttribute("endpoint", "/api/users"),
			stats.WithAttribute("status", "200"),
		)
	}

	// Gauge - current active connections
	client.Gauge("connections.active", 42.0,
		stats.WithAttribute("server", "web-1"),
	)

	// Histogram - response times
	responseTimes := []float64{23.5, 45.2, 12.8, 67.3, 34.1, 89.2, 15.6, 28.9, 52.4, 38.7}
	for _, rt := range responseTimes {
		client.Histogram("response.time", rt,
			stats.WithAttribute("endpoint", "/api/users"),
		)
	}

	// Use convenience methods
	client.Increment("page.views",
		stats.WithAttribute("page", "home"),
	)

	client.IncrementBy("bytes.sent", 1024.0,
		stats.WithAttribute("protocol", "https"),
	)

	// Timing with actual duration
	start := time.Now()
	time.Sleep(50 * time.Millisecond) // Simulate work
	client.Timing("database.query", time.Since(start),
		stats.WithAttribute("query", "SELECT"),
		stats.WithAttribute("table", "users"),
	)

	// High-priority metric
	client.Counter("error.critical", 1.0,
		stats.WithAttribute("error_type", "database_connection"),
		stats.WithPriority(3), // Critical priority
	)

	fmt.Println("✅ Recorded 24 metrics across all backends")
	fmt.Println()

	// Print statistics
	clientStats := client.Stats()
	fmt.Println("Client Statistics:")
	fmt.Printf("  Service: %s\n", clientStats.ServiceName)
	fmt.Printf("  Environment: %s\n", clientStats.Environment)
	fmt.Printf("  Buffer: %d/%d\n", clientStats.Pipeline.BufferLen, clientStats.Pipeline.BufferCap)
	fmt.Printf("  Workers: %d\n", clientStats.Pipeline.Workers)
	fmt.Printf("  Memory Usage: %d bytes\n", clientStats.Pipeline.MemoryUsage)
	fmt.Println()

	// Allow time for metrics to be processed and sent
	fmt.Println("Flushing metrics...")
	time.Sleep(200 * time.Millisecond)

	// Final statistics
	finalStats := client.Stats()
	fmt.Println("\nFinal Statistics:")
	fmt.Printf("  Processed: %d metrics\n", finalStats.Pipeline.Processed)
	fmt.Printf("  Dropped: %d metrics\n", finalStats.Pipeline.Dropped)
	fmt.Printf("  Buffer Dropped: %d metrics\n", finalStats.Pipeline.BufferDropped)
	fmt.Printf("  Errors: %d\n", finalStats.Pipeline.Errors)

	fmt.Println("\n=== Example Complete ===")
	fmt.Println("\nNote: This example assumes you have:")
	fmt.Println("  1. Datadog Agent running on localhost:8125")
	fmt.Println("  2. Prometheus Pushgateway on localhost:9091")
	fmt.Println("  3. CloudWatch Agent on localhost:25888")
	fmt.Println("\nIf backends are not running, metrics will be dropped but the client won't block.")
}
