# Stats - High-Performance OpenTelemetry Stats Library

A production-ready, non-blocking UDP-based stats library for Go with multi-backend support (CloudWatch, Prometheus, Datadog).

## Features

- **Non-Blocking**: Never blocks your application, even under extreme load
- **High Performance**: Lock-free ring buffer, >100k events/sec throughput
- **Multi-Backend**: Support for CloudWatch, Prometheus, and Datadog
- **Zero Allocation**: Object pooling minimizes GC pressure
- **Resilient**: Circuit breakers and graceful degradation
- **Production Ready**: Memory-bounded, tested with race detector

## Quick Start

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

## Installation

```bash
go get github.com/convoy-road-trips-app/stats
```

## Configuration

```go
client, err := stats.NewClient(
    // Service identification
    stats.WithServiceName("my-service"),
    stats.WithEnvironment("production"),

    // Performance tuning
    stats.WithBufferSize(16384),        // Ring buffer capacity
    stats.WithWorkers(4),                // Number of worker goroutines
    stats.WithFlushInterval(100*time.Millisecond),

    // Memory limits
    stats.WithMaxMemoryBytes(10 * 1024 * 1024), // 10MB

    // Backend configuration (Phase 2)
    stats.WithDatadog(&stats.DatadogConfig{
        AgentHost: "localhost",
        AgentPort: 8125,
    }),
)
```

## Architecture

For a detailed overview of the system architecture, see [Architecture Guide](docs/architecture.md).

```
Application → Stats Client → Ring Buffer → Worker Pool → Exporters → UDP → Backends
                              (16K)          (4)          (per backend)
```

## Performance

For detailed performance guidelines and benchmarks, see:
- [Performance Guide](docs/performance_guide.md)
- [Benchmarking Guide](docs/benchmarking.md)
- [Performance Patterns](docs/performance_patterns.md)

| Metric | Target |
|--------|--------|
| Push latency (p99) | <100ns |
| End-to-end (p99) | <100ms |
| Throughput | >100k/sec |
| Memory usage | <10MB |
| Drop rate | <1% |

## API

### Recording Metrics

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

### With Attributes

```go
client.Counter("http.requests", 1.0,
    stats.WithAttribute("method", "POST"),
    stats.WithAttribute("status", "201"),
    stats.WithAttribute("endpoint", "/api/users"),
)
```

### Builder Pattern

```go
metric := stats.NewCounter("user.signup", 1.0).
    WithTag("plan", "premium").
    WithTag("country", "US").
    WithPriority(2).
    Build()

client.RecordMetric(ctx, metric)
```

### Statistics

```go
clientStats := client.Stats()
fmt.Printf("Processed: %d\n", clientStats.Pipeline.Processed)
fmt.Printf("Dropped: %d\n", clientStats.Pipeline.Dropped)
fmt.Printf("Memory: %d bytes\n", clientStats.Pipeline.MemoryUsage)
```

## Testing

```bash
# Run all tests
go test ./...

# Run with race detector
go test -race ./...

# Run benchmarks
go test -bench=. ./transport/...

# Run example
cd examples/basic && go run main.go
```

## Roadmap

### Phase 1: Core Foundation ✅
- [x] Lock-free ring buffer
- [x] UDP connection pool
- [x] Circuit breaker
- [x] Worker pool pipeline
- [x] Public API
- [x] Unit tests

### Phase 2: Backend Exporters (Next)
- [ ] Datadog DogStatsD exporter
- [ ] Prometheus StatsD exporter
- [ ] CloudWatch EMF exporter

### Phase 3: OpenTelemetry Integration
- [ ] MeterProvider setup
- [ ] PeriodicReaders
- [ ] Views for cardinality management

### Phase 4: Production Hardening
- [ ] String interning
- [ ] Binary serialization
- [ ] UDP packet packing
- [ ] Internal metrics collection
- [ ] Integration tests

## Design Principles

1. **Never Block**: Application performance always takes priority
2. **Graceful Degradation**: Drop metrics under pressure rather than fail
3. **Zero Allocation**: Use object pools in hot paths
4. **Isolated Failures**: Backend failures don't affect other backends
5. **Observable**: Library exposes internal metrics

## License

Copyright © 2024 Convoy Road Trips App

## Contributing

See [CLAUDE.md](CLAUDE.md) for development guidelines.
