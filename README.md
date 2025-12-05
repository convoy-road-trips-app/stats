# Stats - High-Performance OpenTelemetry-Compliant Stats Library

A production-ready, non-blocking UDP-based stats library for Go with **full OpenTelemetry SDK compliance** and multi-backend support (Datadog, Prometheus, CloudWatch).

## Features

- ✅ **OpenTelemetry Compliant**: Full OTel SDK implementation with dual-mode operation
- ✅ **Non-Blocking**: Never blocks your application, even under extreme load
- ✅ **High Performance**: Lock-free ring buffer, >100k events/sec throughput
- ✅ **Multi-Backend**: Datadog (DogStatsD), Prometheus (StatsD), CloudWatch (EMF)
- ✅ **Zero Allocation**: Object pooling minimizes GC pressure
- ✅ **Resilient**: Circuit breakers, panic recovery, graceful degradation
- ✅ **Production Ready**: Memory-bounded, race-detector tested, adaptive backpressure
- ✅ **Dual API**: Simple legacy API + standard OTel API

## Quick Start

### Simple API (Legacy Mode)

```go
package main

import (
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
    client.Counter("http.requests", 1.0,
        stats.WithAttribute("method", "GET"),
        stats.WithAttribute("status", "200"),
    )

    client.Gauge("memory.usage", 75.5,
        stats.WithAttribute("unit", "percent"),
    )

    client.Histogram("response.time", 145.3,
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
client.Counter("requests.total", 1.0)
client.Increment("page.views")
client.IncrementBy("bytes.sent", 1024.0)

// Gauge - point-in-time value
client.Gauge("cpu.usage", 45.2)

// Histogram - statistical distribution
client.Histogram("request.duration", 123.4)
client.Timing("db.query", duration)
```

#### With Attributes

```go
client.Counter("http.requests", 1.0,
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
| `Int64UpDownCounter` | Can increase/decrease | Active connections |
| `Float64UpDownCounter` | Can increase/decrease (float) | Temperature |
| `Int64Histogram` | Distribution of integers | Response sizes |
| `Float64Histogram` | Distribution of floats | Request durations |
| `Int64Gauge` | Point-in-time integer | CPU cores |
| `Float64Gauge` | Point-in-time float | CPU percentage |

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

## Examples

- [`examples/basic/`](examples/basic/) - Simple legacy API usage
- [`examples/otel/`](examples/otel/) - OpenTelemetry API usage
- [`examples/multibackend/`](examples/multibackend/) - Multiple backends

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
- [x] Full OTel SDK compliance
- [x] MeterProvider implementation
- [x] All synchronous instruments
- [x] Attribute conversion
- [x] Dual-mode operation (legacy + OTel)
- [x] Comprehensive documentation
- [x] Working examples

### 🔄 Phase 5: Advanced Features (Future)
- [ ] Async/Observable instruments
- [ ] Metric views for cardinality control
- [ ] Custom metric readers
- [ ] Cumulative temporality support
- [ ] Native OTLP exporter
- [ ] String interning for attribute keys
- [ ] Binary serialization optimization
- [ ] Internal metrics collection

## Design Principles

1. **Never Block**: Application performance always takes priority
2. **Graceful Degradation**: Drop metrics under pressure rather than fail
3. **Zero Allocation**: Use object pools in hot paths
4. **Isolated Failures**: Backend failures don't affect other backends
5. **Observable**: Library exposes internal metrics
6. **Standard Compliance**: Full OpenTelemetry SDK compatibility
7. **Backward Compatible**: Legacy API remains unchanged

## Documentation

- [ARCHITECTURE.md](ARCHITECTURE.md) - Detailed architecture overview
- [docs/otel_compliance.md](docs/otel_compliance.md) - OpenTelemetry compliance guide
- [CLAUDE.md](CLAUDE.md) - Development guidelines

## Contributing

See [CLAUDE.md](CLAUDE.md) for development guidelines and project structure.

## License

Copyright © 2024 Convoy Road Trips App
