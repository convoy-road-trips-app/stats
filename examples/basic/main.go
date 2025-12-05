package main

import (
	"context"
	"fmt"
	"time"

	"github.com/convoy-road-trips-app/stats"
)

func main() {
	// Create a new stats client
	client, err := stats.NewClient(
		stats.WithServiceName("example-service"),
		stats.WithEnvironment("development"),
		stats.WithBufferSize(1024),
		stats.WithWorkers(2),
	)
	if err != nil {
		panic(fmt.Sprintf("Failed to create client: %v", err))
	}
	defer client.Close()

	fmt.Println("Stats client created successfully!")
	fmt.Println("Recording metrics...")

	// Record a counter
	if err := client.Counter("http.requests", 1.0,
		stats.WithAttribute("method", "GET"),
		stats.WithAttribute("status", "200"),
	); err != nil {
		fmt.Printf("Error recording counter: %v\n", err)
	}

	// Record a gauge
	if err := client.Gauge("memory.usage", 75.5,
		stats.WithAttribute("unit", "percent"),
	); err != nil {
		fmt.Printf("Error recording gauge: %v\n", err)
	}

	// Record a histogram
	if err := client.Histogram("response.time", 145.3,
		stats.WithAttribute("endpoint", "/api/users"),
	); err != nil {
		fmt.Printf("Error recording histogram: %v\n", err)
	}

	// Use convenience methods
	if err := client.Increment("page.views",
		stats.WithAttribute("page", "home"),
	); err != nil {
		fmt.Printf("Error incrementing counter: %v\n", err)
	}

	// Record timing
	start := time.Now()
	time.Sleep(50 * time.Millisecond) // Simulate work
	if err := client.Timing("operation.duration", time.Since(start),
		stats.WithAttribute("operation", "database_query"),
	); err != nil {
		fmt.Printf("Error recording timing: %v\n", err)
	}

	// Use builder pattern
	metric := stats.NewCounter("user.signup", 1.0).
		WithTag("plan", "premium").
		WithTag("country", "US").
		WithPriority(2).
		Build()

	if err := client.RecordMetric(context.Background(), metric); err != nil {
		fmt.Printf("Error recording metric: %v\n", err)
	}

	// Print statistics
	clientStats := client.Stats()
	fmt.Printf("\nClient Statistics:\n")
	fmt.Printf("  Service: %s\n", clientStats.ServiceName)
	fmt.Printf("  Environment: %s\n", clientStats.Environment)
	fmt.Printf("  Buffer Len: %d/%d\n", clientStats.Pipeline.BufferLen, clientStats.Pipeline.BufferCap)
	fmt.Printf("  Processed: %d\n", clientStats.Pipeline.Processed)
	fmt.Printf("  Dropped: %d\n", clientStats.Pipeline.Dropped)
	fmt.Printf("  Errors: %d\n", clientStats.Pipeline.Errors)
	fmt.Printf("  Memory Usage: %d bytes\n", clientStats.Pipeline.MemoryUsage)

	// Allow time for metrics to be processed
	time.Sleep(200 * time.Millisecond)

	// Final statistics
	finalStats := client.Stats()
	fmt.Printf("\nFinal Statistics:\n")
	fmt.Printf("  Processed: %d\n", finalStats.Pipeline.Processed)
	fmt.Printf("  Buffer Dropped: %d\n", finalStats.Pipeline.BufferDropped)

	fmt.Println("\nShutting down gracefully...")
}
