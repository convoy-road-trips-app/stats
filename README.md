# Stats - High-Performance OpenTelemetry-Compliant Stats Library

A production-ready, non-blocking stats library for Go that implements the **OpenTelemetry Metrics API** and exports to multiple backends (Datadog, Prometheus, CloudWatch, OTLP).

## Features

- ✅ **OpenTelemetry Metrics API**: Sync and observable instruments, dual-mode operation, cumulative OTLP export with explicit-bucket histograms and exemplars ([details and limitations](docs/otel_compliance.md))
- ✅ **Non-Blocking**: Never blocks your application, even under extreme load
- ✅ **High Performance**: Lock-free ring buffer, >100k events/sec throughput
- ✅ **Multi-Backend**: Datadog (DogStatsD), Prometheus (StatsD), CloudWatch (EMF)
- ✅ **Zero Allocation**: Object pooling minimizes GC pressure
- ✅ **Resilient**: Circuit breakers, panic recovery, graceful degradation
- ✅ **Production Ready**: Memory-bounded, race-detector tested, adaptive backpressure
- ✅ **Dual API**: Simple legacy API + standard OTel API
- ✅ **Runtime Metrics**: Automatic Go CPU, heap, GC, and goroutine telemetry

## Quick Start

### Simple API (Legacy Mode)

```go
package main

import (
    "context"

    "github.com/convoy-road-trips-app/stats"
)

func main() {
    // Create client
    client, err := stats.NewClient(
        stats.WithServiceName("my-service"),
        stats.WithEnvironment("production"),
        stats.WithDatadog(&stats.DatadogConfig{
            AgentHost: "localhost",
            AgentPort: 8125,
        }),
    )
    if err != nil {
        panic(err)
    }
    defer client.Close()

    // Record metrics - never blocks!
    ctx := context.Background()
    client.Counter(ctx, "http.requests", 1.0,
        stats.WithAttribute("method", "GET"),
        stats.WithAttribute("status", "200"),
    )

    client.Gauge(ctx, "memory.usage", 75.5,
        stats.WithAttribute("unit", "percent"),
    )

    client.Histogram(ctx, "response.time", 0.145,
        stats.WithAttribute("endpoint", "/api/users"),
    )
}
```

### OpenTelemetry API (OTel Mode)

```go
package main

import (
    "context"
    
    "go.opentelemetry.io/otel/attribute"
    "go.opentelemetry.io/otel/metric"
    
    "github.com/convoy-road-trips-app/stats"
    "github.com/convoy-road-trips-app/stats/otel"
)

func main() {
    // Create OTel-compliant MeterProvider
    provider, err := otel.NewMeterProvider(
        otel.WithStatsOptions(
            stats.WithServiceName("my-service"),
            stats.WithEnvironment("production"),
            stats.WithDatadog(&stats.DatadogConfig{
                AgentHost: "localhost",
                AgentPort: 8125,
            }),
        ),
    )
    if err != nil {
        panic(err)
    }
    defer provider.Shutdown(context.Background())

    // Use standard OTel API
    meter := provider.Meter("my-app")
    
    // Create instruments
    counter, _ := meter.Int64Counter("http.requests")
    histogram, _ := meter.Float64Histogram("response.time")
    gauge, _ := meter.Float64Gauge("memory.usage")

    // Record metrics
    ctx := context.Background()
    counter.Add(ctx, 1, metric.WithAttributes(
        attribute.String("method", "GET"),
        attribute.String("status", "200"),
    ))
    
    histogram.Record(ctx, 145.3, metric.WithAttributes(
        attribute.String("endpoint", "/api/users"),
    ))
    
    gauge.Record(ctx, 75.5, metric.WithAttributes(
        attribute.String("unit", "percent"),
    ))
}
```

**Both modes share the same high-performance pipeline!** See [docs/otel_compliance.md](docs/otel_compliance.md) for details.

## Installation

```bash
go get github.com/convoy-road-trips-app/stats
```

## Configuration

### Basic Configuration

```go
client, err := stats.NewClient(
    // Service identification
    stats.WithServiceName("my-service"),
    stats.WithEnvironment("production"),

    // Performance tuning
    stats.WithBufferSize(16384),        // Ring buffer capacity (default: 8192)
    stats.WithWorkers(4),                // Worker goroutines (default: 2)
    stats.WithFlushInterval(100*time.Millisecond),

    // Memory limits
    stats.WithMaxMemoryBytes(10 * 1024 * 1024), // 10MB

    // Backpressure handling
    stats.WithDropStrategy(stats.DropOldest),   // Drop oldest on overflow
    stats.WithAdaptiveBatching(true),           // Increase batch size under load
)
```

### Backend Configuration

#### Datadog (DogStatsD)

```go
stats.WithDatadog(&stats.DatadogConfig{
    AgentHost: "localhost",
    AgentPort: 8125,
    Tags:      []string{"env:prod", "version:1.0"},
})
```

#### Prometheus (StatsD)

```go
stats.WithPrometheus(&stats.PrometheusConfig{
    Host:   "localhost",
    Port:   9125,
    Prefix: "myapp",
})
```

#### CloudWatch (EMF)

```go
stats.WithCloudWatch(&stats.CloudWatchConfig{
    LogGroupName:  "/aws/ecs/my-service",
    Namespace:     "MyApp/Metrics",
    FlushInterval: 60 * time.Second,
})
```

#### OTLP (gRPC)

```go
stats.WithOTLP(&stats.OTLPConfig{
    Endpoint:    "localhost:4317",
    Insecure:    true,
    ServiceName: "my-service",
    Headers:     map[string]string{"Authorization": "Bearer token"},
})
```

#### OTLP (HTTP)

```go
stats.WithOTLP(&stats.OTLPConfig{
    Endpoint:    "localhost:4318",
    Insecure:    true,
    ServiceName: "my-service",
    Protocol:    stats.OTLPProtocolHTTP,
})
```

When `Enabled` is false (or the config is omitted), no connection is established and the exporter is a no-op. `Protocol` defaults to `"grpc"` if unset.

OTLP export semantics (v1.1.0):

- Counters and histograms are **cumulative** by default (`stats.WithTemporality(stats.Delta)` opts out; Prometheus' OTLP receiver drops delta series).
- Histograms use explicit buckets: by default the D9 seconds bounds `0.005 … 10`, overridable with `stats.WithHistogramBuckets(...)`. In Prometheus they appear as `_bucket{le=...}`, `_count` and `_sum`.
- `service.name`, `deployment.environment` and `service.version` come from options, `OTEL_SERVICE_NAME` / `DEPLOYMENT_ENVIRONMENT` / `SERVICE_VERSION`, or `OTEL_RESOURCE_ATTRIBUTES`.
- Counter and histogram observations recorded under a sampled span carry `trace_id`/`span_id` exemplars.
- `stats.WithOTLPRetry(...)` retries retryable failures. Background exports are bounded by `WithUDPTimeout` (100 ms default); raise it for remote collectors.

#### Attribute and cardinality limits (all backends)

- Attribute keys must match `^[a-zA-Z_][a-zA-Z0-9_]*$`, otherwise the observation is rejected with `stats.ErrInvalidTagKey` (use `http_method`, not `http.method`).
- Values are capped at 256 runes, and only the first 10 keys in lexical order are kept.
- At most 2000 attribute sets per metric name by default (`stats.WithMaxCardinality`); new series beyond that return `stats.ErrCardinalityLimit`.
- Drops are counted in `telemetry_dropped_labels_total{reason}`.

#### Flush and shutdown

`client.Flush(ctx)` and `provider.ForceFlush(ctx)` export everything buffered with the caller's context; `Shutdown(ctx)` drains the buffer before returning. Use them at the end of each AWS Lambda invocation.

#### Runtime Metrics (Go CPU + Heap)

Enable automatic Go runtime telemetry with a single option:

```go
// Legacy Mode
client, err := stats.NewClient(
    stats.WithServiceName("my-service"),
    stats.WithRuntimeMetrics(), // collects every 10s, prefix "runtime.go"
)

// OTel Mode
provider, err := otel.NewMeterProvider(
    otel.WithStatsOptions(
        stats.WithServiceName("my-service"),
        stats.WithRuntimeMetrics(),
    ),
)
```

**Collected metrics** (prefix `runtime.go.`):

| Group | Example Metrics |
|-------|----------------|
| Memory | `memory.heap.alloc`, `memory.heap.inuse`, `memory.sys` |
| Heap | `heap.allocs.bytes`, `heap.objects.live`, `heap.goal.bytes` |
| GC | `gc.cycles.total`, `gc.cpu.seconds` |
| Scheduler | `goroutines`, `gomaxprocs`, `cgo.calls` |
| CPU time | `cpu.total.seconds`, `cpu.user.seconds`, `cpu.idle.seconds` |

All values are emitted as absolute gauges. See [docs/runtime_metrics.md](docs/runtime_metrics.md) for the full metric list, semantics, and how to compute rates in your backend.

## Architecture

For a detailed overview of the system architecture, see [ARCHITECTURE.md](ARCHITECTURE.md).

```
┌─────────────────────────────────────────────────────────────┐
│                    Application Code                          │
└───────────────┬─────────────────────────────────────────────┘
                │
                ├─ Legacy Mode: stats.NewClient()
                │  └─> client.Counter(), client.Gauge(), etc.
                │
                └─ OTel Mode: otel.NewMeterProvider()
                   └─> meter.Int64Counter(), meter.Float64Histogram(), etc.
                │
                ▼
┌───────────────────────────────────────────────────────────────┐
│              High-Performance Pipeline (Shared)                │
│  • Lock-free ring buffer (16K capacity)                       │
│  • Worker pool (4 workers) with parallel exporting            │
│  • Adaptive batching & backpressure handling                  │
│  • Panic recovery & per-exporter error tracking               │
└───────────────┬───────────────────────────────────────────────┘
                │
                ▼
┌───────────────────────────────────────────────────────────────┐
│                    Backend Exporters                           │
│  • Datadog (DogStatsD over UDP)                               │
│  • Prometheus (StatsD over UDP)                               │
│  • CloudWatch (EMF via logs)                                  │
│  • OTLP (gRPC to OTel Collector)                              │
└───────────────────────────────────────────────────────────────┘
```

### Key Components

- **Ring Buffer**: Lock-free, bounded MPSC queue using atomic operations
- **Worker Pool**: Parallel metric processing with configurable workers
- **Exporters**: Backend-specific serialization and transport
- **Circuit Breaker**: Protects against cascading failures
- **UDP Pool**: Pre-allocated connections for zero-allocation sends

## Performance

| Metric | Target | Actual |
|--------|--------|--------|
| Push latency (p99) | <100ns | ~25ns |
| End-to-end (p99) | <100ms | <50ms |
| Throughput | >100k/sec | >200k/sec |
| Memory usage | <10MB | ~5MB |
| Drop rate | <1% | <0.1% |
| OTel overhead | - | +13% (~3ns) |

### Benchmarks

```
BenchmarkRingBuffer/Push-8           50000000    25.3 ns/op    0 B/op    0 allocs/op
BenchmarkRingBuffer/Pop-8            50000000    28.7 ns/op    0 B/op    0 allocs/op
BenchmarkOTelMode/Counter-8          45000000    28.7 ns/op    0 B/op    0 allocs/op
```

See [docs/performance_guide.md](docs/performance_guide.md) for detailed performance analysis.

## API Reference

### Legacy API

#### Recording Metrics

```go
// Counter - monotonically increasing value
client.Counter(ctx, "requests.total", 1.0)
client.Increment(ctx, "page.views")
client.IncrementBy(ctx, "bytes.sent", 1024.0)

// Gauge - point-in-time value
client.Gauge(ctx, "cpu.usage", 45.2)

// Histogram - statistical distribution
client.Histogram(ctx, "request.duration", 0.1234) // seconds, matches the default buckets
client.Timing(ctx, "db.query", duration)          // records milliseconds
```

#### With Attributes

```go
client.Counter(ctx, "http.requests", 1.0,
    stats.WithAttribute("method", "POST"),
    stats.WithAttribute("status", "201"),
    stats.WithAttribute("endpoint", "/api/users"),
)
```

#### Statistics

```go
clientStats := client.Stats()
fmt.Printf("Processed: %d\n", clientStats.Pipeline.Processed)
fmt.Printf("Dropped: %d\n", clientStats.Pipeline.Dropped)
fmt.Printf("Errors: %d\n", clientStats.Pipeline.Errors)
fmt.Printf("Buffer Length: %d\n", clientStats.Pipeline.BufferLength)
fmt.Printf("Exporter Errors: %v\n", clientStats.Pipeline.ExporterErrors)
```

### OpenTelemetry API

#### Supported Instruments

| Instrument | Description | Example Use Case |
|------------|-------------|------------------|
| `Int64Counter` | Monotonically increasing integer | Request counts |
| `Float64Counter` | Monotonically increasing float | Fractional increments |
| `Int64UpDownCounter` | Can increase/decrease (exported as a gauge of the latest increment) | Active connections |
| `Float64UpDownCounter` | Can increase/decrease (exported as a gauge of the latest increment) | Temperature |
| `Int64Histogram` | Distribution of integers | Response sizes |
| `Float64Histogram` | Distribution of floats | Request durations |
| `Int64Gauge` | Point-in-time integer | CPU cores |
| `Float64Gauge` | Point-in-time float | CPU percentage |
| `Int64/Float64ObservableCounter` | Callback-reported cumulative total | Bytes read since start |
| `Int64/Float64ObservableUpDownCounter` | Callback-reported value, exported as a gauge | Queue depth |
| `Int64/Float64ObservableGauge` | Callback-reported point-in-time value | Pool utilization |

#### Creating Instruments

```go
meter := provider.Meter("my-app")

counter, _ := meter.Int64Counter("requests",
    metric.WithDescription("Total requests"),
    metric.WithUnit("{request}"),
)

histogram, _ := meter.Float64Histogram("duration",
    metric.WithDescription("Request duration"),
    metric.WithUnit("ms"),
)

gauge, _ := meter.Float64Gauge("memory",
    metric.WithDescription("Memory usage"),
    metric.WithUnit("By"),
)
```

See [docs/otel_compliance.md](docs/otel_compliance.md) for complete OTel documentation.

## Testing

### Using Makefile

```bash
# Run all tests
make test

# Run with race detector
make test-race

# Run benchmarks
make bench

# Run linter
make lint

# OTLP -> collector -> Prometheus integration tests against grafana/otel-lgtm (requires Docker)
make lgtm-test

# Build
make build

# Clean
make clean
```

### Manual Testing

```bash
# Run all tests
go test ./...

# Run with race detector
go test -race ./...

# Run benchmarks
go test -bench=. ./transport/...

# Run examples
go run examples/basic/main.go
go run examples/otel/main.go
go run examples/multibackend/main.go
```

## Testing & Mocking

The library provides a `Recorder` interface and a `NoOpClient` to facilitate testing your application without sending real metrics.

### Using the Recorder Interface

Instead of depending on the concrete `*stats.Client`, your services should depend on the `stats.Recorder` interface:

```go
type MyService struct {
    stats stats.Recorder
}

func NewMyService(s stats.Recorder) *MyService {
    return &MyService{stats: s}
}
```

### Mocking in Tests

In your unit tests, you can use `stats.NewNoOpClient()` which implements the `Recorder` interface but performs no operations:

```go
func TestMyService(t *testing.T) {
    // Non-blocking no-op client for testing
    mockStats := stats.NewNoOpClient()
    svc := NewMyService(mockStats)

    // Run your tests...
}
```

See [examples/testing/](examples/testing/) for a complete example.

## Examples

- [`examples/basic/`](examples/basic/) - Simple legacy API usage
- [`examples/otel/`](examples/otel/) - OpenTelemetry API usage
- [`examples/multibackend/`](examples/multibackend/) - Multiple backends
- [`examples/testing/`](examples/testing/) - Testing & Mocking guide

## Roadmap

### ✅ Phase 1: Core Foundation (Complete)
- [x] Lock-free ring buffer with atomic operations
- [x] UDP connection pool
- [x] Circuit breaker pattern
- [x] Worker pool pipeline
- [x] Public API with builder pattern
- [x] Comprehensive unit tests
- [x] Race detector validation

### ✅ Phase 2: Backend Exporters (Complete)
- [x] Datadog DogStatsD exporter
- [x] Prometheus StatsD exporter
- [x] CloudWatch EMF exporter
- [x] Serializer abstraction
- [x] Per-exporter error tracking

### ✅ Phase 3: Robustness & Performance (Complete)
- [x] Adaptive batching
- [x] Drop strategies (DropNewest, DropOldest)
- [x] Parallel exporting with bulkheading
- [x] Panic recovery in exporters
- [x] Memory limits and backpressure
- [x] Makefile for common tasks

### ✅ Phase 4: OpenTelemetry Integration (Complete)
- [x] OpenTelemetry Metrics API implementation
- [x] MeterProvider implementation
- [x] All synchronous instruments
- [x] Attribute conversion
- [x] Dual-mode operation (legacy + OTel)
- [x] Comprehensive documentation
- [x] Working examples

### 🔄 Phase 5: Advanced Features (Future)
- [x] Async/Observable instruments (v1.1.0)
- [ ] Metric views for cardinality control
- [ ] Custom metric readers
- [x] Cumulative temporality support (v1.1.0, OTLP default)
- [x] Native OTLP exporter
- [ ] String interning for attribute keys
- [ ] Binary serialization optimization
- [x] Runtime metrics collection (CPU, heap, GC, goroutines)

## Design Principles

1. **Never Block**: Application performance always takes priority
2. **Graceful Degradation**: Drop metrics under pressure rather than fail
3. **Zero Allocation**: Use object pools in hot paths
4. **Isolated Failures**: Backend failures don't affect other backends
5. **Observable**: Library exposes internal metrics
6. **Standard API**: Implements the OpenTelemetry Metrics API (see [limitations](docs/otel_compliance.md#limitations))
7. **Backward Compatible**: Legacy API remains unchanged

## Documentation

- [ARCHITECTURE.md](ARCHITECTURE.md) - Detailed architecture overview
- [docs/otel_compliance.md](docs/otel_compliance.md) - OpenTelemetry compliance guide
- [CLAUDE.md](CLAUDE.md) - Development guidelines

## Contributing

See [CLAUDE.md](CLAUDE.md) for development guidelines and project structure.

## License

Copyright © 2024 Convoy Road Trips App
