package main

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/convoy-road-trips-app/stats"
	"github.com/convoy-road-trips-app/stats/otel"
)

func main() {
	fmt.Println("=== OpenTelemetry Stats Example ===")
	fmt.Println()

	// Create an OTel-compliant MeterProvider
	provider, err := otel.NewMeterProvider(
		otel.WithStatsOptions(
			stats.WithServiceName("otel-demo"),
			stats.WithEnvironment("development"),
			stats.WithBufferSize(1024),
			stats.WithWorkers(2),

			// Enable Datadog backend
			stats.WithDatadog(&stats.DatadogConfig{
				AgentHost: "localhost",
				AgentPort: 8125,
				Tags:      []string{"env:dev", "app:otel-demo"},
			}),
		),
	)
	if err != nil {
		panic(err)
	}
	defer provider.Shutdown(context.Background())

	fmt.Println("✓ Created OTel MeterProvider")

	// Get a meter (equivalent to a tracer in tracing)
	meter := provider.Meter(
		"github.com/convoy-road-trips-app/stats/examples/otel",
		metric.WithInstrumentationVersion("1.0.0"),
	)

	fmt.Println("✓ Created Meter")

	// Create instruments
	httpRequestCounter, err := meter.Int64Counter(
		"http.server.requests",
		metric.WithDescription("Total HTTP requests"),
		metric.WithUnit("{request}"),
	)
	if err != nil {
		panic(err)
	}

	requestDuration, err := meter.Float64Histogram(
		"http.server.duration",
		metric.WithDescription("HTTP request duration"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		panic(err)
	}

	activeConnections, err := meter.Int64Gauge(
		"http.server.active_connections",
		metric.WithDescription("Active HTTP connections"),
		metric.WithUnit("{connection}"),
	)
	if err != nil {
		panic(err)
	}

	memoryUsage, err := meter.Float64Gauge(
		"process.memory.usage",
		metric.WithDescription("Process memory usage"),
		metric.WithUnit("By"),
	)
	if err != nil {
		panic(err)
	}

	fmt.Println("✓ Created instruments")
	fmt.Println()

	// Simulate some application activity
	ctx := context.Background()

	fmt.Println("Recording metrics...")

	// Simulate HTTP requests
	for i := 0; i < 10; i++ {
		method := "GET"
		if i%3 == 0 {
			method = "POST"
		}

		status := "200"
		if i%7 == 0 {
			status = "404"
		}

		// Record request
		httpRequestCounter.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("http.method", method),
				attribute.String("http.status_code", status),
				attribute.String("http.route", "/api/users"),
			),
		)

		// Record duration
		duration := float64(50 + i*10)
		requestDuration.Record(ctx, duration,
			metric.WithAttributes(
				attribute.String("http.method", method),
				attribute.String("http.route", "/api/users"),
			),
		)

		fmt.Printf("  Request %d: %s %s (%.0fms)\n", i+1, method, status, duration)
	}

	// Record gauge values
	activeConnections.Record(ctx, 42,
		metric.WithAttributes(attribute.String("state", "active")),
	)

	memoryUsage.Record(ctx, 1024*1024*128, // 128 MB
		metric.WithAttributes(attribute.String("type", "heap")),
	)

	fmt.Println()
	fmt.Println("✓ Recorded 10 HTTP requests")
	fmt.Println("✓ Recorded gauge values")

	// Give time for metrics to be flushed
	time.Sleep(200 * time.Millisecond)

	fmt.Println()
	fmt.Println("=== Example Complete ===")
	fmt.Println()
	fmt.Println("Note: This example uses the OpenTelemetry Metrics API")
	fmt.Println("Metrics are sent to configured backends (e.g., Datadog)")
	fmt.Println("The API is fully OTel-compliant and can be used with any OTel tooling")
}
