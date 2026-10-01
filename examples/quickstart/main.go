package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/convoy-road-trips-app/stats"
)

func main() {
	// Build the client once at startup. Without a backend option it records
	// but exports nowhere; here it pushes OTLP/HTTP to a local collector.
	client, err := stats.NewClient(
		stats.WithServiceName("checkout"),
		stats.WithEnvironment("production"),
		stats.WithOTLP(&stats.OTLPConfig{
			Endpoint: "localhost:4318",
			Insecure: true,
			Protocol: stats.OTLPProtocolHTTP,
		}),
		// Background exports are bounded by this timeout (default 100 ms);
		// raise it for a remote collector.
		stats.WithUDPTimeout(5*time.Second),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "create client:", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// Recording never blocks. The error reports a dropped observation (full
	// buffer, cardinality limit, invalid attribute key).
	if err := client.Counter(ctx, "checkout_orders", 1,
		stats.WithAttribute("payment", "card"),
	); err != nil {
		fmt.Fprintln(os.Stderr, "counter:", err)
	}
	_ = client.Gauge(ctx, "checkout_queue_depth", 12)
	_ = client.Histogram(ctx, "checkout_latency_seconds", 0.145, // seconds
		stats.WithAttributes(map[string]string{"route": "/pay", "http.method": "POST"}),
	)

	// Export everything recorded so far, for example at the end of a Lambda
	// invocation. Flush is also available as the stats.Flusher interface.
	flushCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Flush(flushCtx); err != nil {
		fmt.Fprintln(os.Stderr, "flush:", err)
	}

	// Shutdown drains whatever is still buffered. Call it once, on exit.
	if err := client.Shutdown(flushCtx); err != nil {
		fmt.Fprintln(os.Stderr, "shutdown:", err)
	}
}
