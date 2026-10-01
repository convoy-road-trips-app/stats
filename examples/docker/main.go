// Command docker is the demo service of examples/docker: it records a counter,
// a gauge and a latency histogram and exports them over OTLP/HTTP to a
// collector. See README.md in this directory.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/convoy-road-trips-app/stats"
)

// Metric names. Underscores keep the Prometheus names identical to them.
const (
	counterName   = "demo_requests"
	gaugeName     = "demo_queue_depth"
	histogramName = "demo_request_duration_seconds"
)

// shutdownTimeout bounds the final drain of buffered metrics.
const shutdownTimeout = 10 * time.Second

type config struct {
	Endpoint string        // OTLP/HTTP host:port of the collector
	Service  string        // service.name resource attribute
	Interval time.Duration // time between simulated requests
}

// configFromEnv reads OTLP_ENDPOINT, SERVICE_NAME and INTERVAL.
func configFromEnv(getenv func(string) string) (config, error) {
	cfg := config{
		Endpoint: valueOr(getenv("OTLP_ENDPOINT"), "localhost:4318"),
		Service:  valueOr(getenv("SERVICE_NAME"), "stats-docker-example"),
		Interval: 200 * time.Millisecond,
	}
	if raw := getenv("INTERVAL"); raw != "" {
		interval, err := time.ParseDuration(raw)
		if err != nil || interval <= 0 {
			return config{}, fmt.Errorf("INTERVAL %q: want a positive duration such as 200ms", raw)
		}
		cfg.Interval = interval
	}
	return cfg, nil
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func main() {
	os.Exit(realMain())
}

// realMain returns the exit code, so deferred calls run before os.Exit.
func realMain() int {
	cfg, err := configFromEnv(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "demo:", err)
		return 1
	}
	return 0
}

// run records metrics every cfg.Interval until ctx ends, then drains them with
// Shutdown.
func run(ctx context.Context, cfg config) error {
	client, err := stats.NewClient(
		stats.WithServiceName(cfg.Service),
		stats.WithEnvironment("docker"),
		stats.WithOTLP(&stats.OTLPConfig{
			Endpoint: cfg.Endpoint,
			Insecure: true,
			Protocol: stats.OTLPProtocolHTTP,
		}),
		stats.WithFlushInterval(time.Second),
		// Background exports are bounded by this timeout (default 100 ms),
		// which is too short for a collector behind a container network.
		stats.WithUDPTimeout(5*time.Second),
	)
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}

	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	for i := 0; ctx.Err() == nil; i++ {
		recordRequest(ctx, client, i)
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}

	// ctx is done, so drain with a fresh deadline-bound context.
	drain, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := client.Shutdown(drain); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

// routes and latencies are cycled through, so the demo is deterministic. The
// latencies are in seconds: mostly fast requests with a slow tail, spread over
// several of the default buckets.
var (
	routes    = [...]string{"/users", "/orders", "/health"}
	latencies = [...]float64{0.004, 0.008, 0.02, 0.03, 0.045, 0.08, 0.12, 0.2, 0.4, 0.9, 3, 12}
)

// recordRequest simulates request number i. Recording never blocks; a full
// buffer or a cardinality limit is reported as an error, which a service
// would usually count rather than act on.
func recordRequest(ctx context.Context, client stats.Recorder, i int) {
	route := routes[i%len(routes)]
	err := errors.Join(
		client.Counter(ctx, counterName, 1, stats.WithAttribute("route", route)),
		client.Histogram(ctx, histogramName, latencies[i%len(latencies)], stats.WithAttribute("route", route)),
		client.Gauge(ctx, gaugeName, float64(i%20)),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "record:", err)
	}
}
