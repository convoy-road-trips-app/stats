# Install and usage

## Install

```bash
go get github.com/convoy-road-trips-app/stats@v1.2.1
```

Requires Go 1.26 or newer. Upgrading from v1.0.x? v1.1.0 changes some behavior (cumulative OTLP by default, key rejection, cardinality limits, draining `Shutdown`); see [Upgrading from v1.0.x](../README.md#upgrading-from-v10x-semver-exception).

## Minimal usage

Create one client at startup, record from anywhere, flush when an invocation ends, and shut down on exit. This program is `examples/quickstart/main.go`; a test keeps this copy identical to it, and CI builds it.

```go
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
```

| Call | Records | Notes |
|---|---|---|
| `Counter(ctx, name, v, opts...)`, `Increment`, `IncrementBy` | a sum | cumulative in OTLP by default |
| `Gauge(ctx, name, v, opts...)` | the latest value | |
| `Histogram(ctx, name, v, opts...)` | one observation | explicit buckets in OTLP; the default bounds are in **seconds** (`0.005 ... 10`) |
| `Timing(ctx, name, d, opts...)` | a duration in **milliseconds** | does not match the default seconds buckets; record seconds with `Histogram` instead |
| `stats.WithAttribute(k, v)`, `stats.WithAttributes(map)` | attributes (tags) | see the limits below |

### Attributes

- Keys are identifier segments joined by single dots (`route`, `http.method`); anything else returns `ErrInvalidTagKey`.
- At most 10 attributes per observation, values capped at 256 runes, and 2000 series per metric name (`WithMaxCardinality`); a new series beyond that returns `ErrCardinalityLimit` and is counted in `telemetry_dropped_labels_total{reason}`.
- Do not put unbounded values (user IDs, request IDs) in attributes.

### Flush and shutdown

- `Flush(ctx)` exports everything recorded before the call and returns when it is exported or `ctx` is done. Call it at the end of a Lambda invocation.
- `Shutdown(ctx)` / `Close()` stop accepting observations and drain the buffer. Always call it before the process exits, or buffered metrics are lost.
- `Flush` is on `*Client` and `*NoOpClient` through the optional `stats.Flusher` interface; `stats.Recorder` has no `Flush`, so existing implementations keep compiling: `if f, ok := recorder.(stats.Flusher); ok { _ = f.Flush(ctx) }`.

### Testing code that records metrics

Accept a `stats.Recorder` and pass `stats.NewNoOpClient()` in tests (see `examples/testing`).

## Configuration reference

Options are passed to `stats.NewClient`. Defaults are those of `stats.DefaultConfig()`.

### Core

| Option | Default | Purpose |
|---|---|---|
| `WithServiceName(name)` | `unknown-service` | `service.name`; also read from `OTEL_SERVICE_NAME` for OTLP |
| `WithEnvironment(env)` | `development` | `deployment.environment` |
| `WithBufferSize(n)` | 16384 | ring buffer capacity (rounded up to a power of two, minimum 2) |
| `WithWorkers(n)` | 4 | goroutines that batch and export |
| `WithFlushInterval(d)` | 100 ms | how often a worker exports its batch |
| `WithUDPTimeout(d)` | 100 ms | bound for each **background** export, OTLP included; raise it for a remote collector |
| `WithMaxMemoryBytes(n)` | 10 MiB | memory cap for buffered metrics (`ErrMemoryLimit`) |
| `WithMaxCardinality(n)` | 2000 | series per metric name |
| `WithDropStrategy(s)` | `DropNewest` | `DropNewest` rejects the incoming metric on a full buffer; `DropOldest` evicts the oldest |
| `WithAdaptiveBatching(bool)` | off | larger batches under load |
| `WithRateLimit(rate, burst)` | off | cap on observations per second |
| `WithRuntimeMetrics()` | off | Go runtime metrics every 10 s |

### OTLP

| Option | Default | Purpose |
|---|---|---|
| `WithOTLP(&OTLPConfig{...})` | disabled | enables OTLP: `Endpoint` (`host:port`), `Insecure`, `Headers`, `Protocol` (`OTLPProtocolGRPC` default, port 4317; `OTLPProtocolHTTP`, port 4318), `ExportTimeout` (10 s) |
| `WithTemporality(stats.Cumulative \| stats.Delta)` | `Cumulative` | Prometheus' OTLP receiver only ingests cumulative |
| `WithHistogramBuckets([]float64)` | `0.005 ... 10` | explicit bounds in the metric's unit; finite and strictly increasing |
| `WithOTLPRetry(initial, max, maxElapsed)` | SDK default | retry retryable failures |
| `WithOTLPResourceAttributes(attrs...)` | none | extra resource attributes; `OTEL_RESOURCE_ATTRIBUTES` is also read |

### Other backends

`WithDatadog(&DatadogConfig{AgentHost, AgentPort, Tags})`, `WithPrometheus(&PrometheusConfig{Host, Port, Prefix})` (StatsD) and `WithCloudWatch(&CloudWatchConfig{LogGroupName, Namespace, ...})` can be combined with OTLP; each receives every observation.

## Run it end to end

[examples/docker](../examples/docker/README.md) starts a demo service, an OpenTelemetry Collector and Prometheus with `docker compose up --build`, and shows the `_bucket` series.

## More

- [README](../README.md): backends, OTel API mode, runtime metrics.
- [docs/otel_compliance.md](otel_compliance.md): OTLP semantics, SemVer exception and known limitations.
- [CHANGELOG](../CHANGELOG.md).
