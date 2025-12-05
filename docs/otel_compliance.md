# OpenTelemetry Compliance

This document describes the OpenTelemetry (OTel) compliance features of the stats library.

## Overview

The stats library provides **dual-mode operation**:

1. **Legacy Mode** (default): Simple, high-performance API (`stats.NewClient()`)
2. **OTel Mode**: Full OpenTelemetry SDK compliance (`otel.NewMeterProvider()`)

Both modes share the same high-performance pipeline underneath, ensuring consistent performance characteristics.

## Quick Start

### Using OTel Mode

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
    // Create an OTel-compliant MeterProvider
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

    // Get a meter
    meter := provider.Meter("my-app")

    // Create instruments
    counter, _ := meter.Int64Counter("requests.total")
    histogram, _ := meter.Float64Histogram("request.duration")

    // Record metrics
    ctx := context.Background()
    counter.Add(ctx, 1, 
        metric.WithAttributes(
            attribute.String("method", "GET"),
            attribute.String("status", "200"),
        ),
    )

    histogram.Record(ctx, 123.45,
        metric.WithAttributes(
            attribute.String("endpoint", "/api/users"),
        ),
    )
}
```

## Supported Instruments

### Synchronous Instruments

All synchronous instruments are fully supported:

| Instrument | Description | Use Case |
|------------|-------------|----------|
| `Int64Counter` | Monotonically increasing integer | Request counts, bytes sent |
| `Float64Counter` | Monotonically increasing float | Fractional increments |
| `Int64UpDownCounter` | Can increase or decrease | Active connections, queue size |
| `Float64UpDownCounter` | Can increase or decrease (float) | Temperature changes |
| `Int64Histogram` | Distribution of integer values | Response sizes |
| `Float64Histogram` | Distribution of float values | Request durations, latencies |
| `Int64Gauge` | Point-in-time integer value | CPU usage, memory |
| `Float64Gauge` | Point-in-time float value | CPU percentage, ratios |

### Asynchronous Instruments

Asynchronous (observable) instruments are **not supported** due to OpenTelemetry SDK limitations. The Go OTel SDK uses unexported marker methods (`int64Observable`, `float64Observable`) that prevent external implementations of these interfaces.

Attempting to create these instruments will return an error:
- `Int64ObservableCounter`
- `Float64ObservableCounter`
- `Int64ObservableUpDownCounter`
- `Float64ObservableUpDownCounter`
- `Int64ObservableGauge`
- `Float64ObservableGauge`
- `RegisterCallback`

> **Note**: Most applications only need synchronous instruments (Counter, Histogram, Gauge). If you require asynchronous instruments (e.g., for scraping system metrics), we recommend using the official OpenTelemetry SDK alongside this library.

## Architecture

### How It Works

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
│  • Lock-free ring buffer                                      │
│  • Worker pool with parallel exporting                        │
│  • Adaptive batching & backpressure handling                  │
│  • Panic recovery & per-exporter error tracking               │
└───────────────┬───────────────────────────────────────────────┘
                │
                ▼
┌───────────────────────────────────────────────────────────────┐
│                    Backend Exporters                           │
│  • Datadog (DogStatsD)                                        │
│  • Prometheus (StatsD)                                        │
│  • CloudWatch (EMF)                                           │
└───────────────────────────────────────────────────────────────┘
```

### Key Design Decisions

1. **No Aggregation in OTel Mode**: Metrics are passed directly to the underlying stats client, which handles batching and export. This maintains our high-performance characteristics.

2. **Attribute Conversion**: OTel `attribute.Set` is converted to stats `MetricOption` format using an iterator to avoid allocations.

3. **Embedded Types**: We use `embedded.Meter`, `embedded.Int64Counter`, etc. to satisfy OTel SDK interfaces without implementing marker methods.

4. **Shared Pipeline**: Both modes use the same lock-free ring buffer and worker pool, ensuring consistent performance.

## Migration Guide

### From Legacy to OTel Mode

**Before (Legacy Mode):**
```go
client, _ := stats.NewClient(
    stats.WithServiceName("my-service"),
)
defer client.Close()

client.Counter("requests", 1.0,
    stats.WithAttribute("method", "GET"),
)
```

**After (OTel Mode):**
```go
provider, _ := otel.NewMeterProvider(
    otel.WithStatsOptions(
        stats.WithServiceName("my-service"),
    ),
)
defer provider.Shutdown(context.Background())

meter := provider.Meter("my-app")
counter, _ := meter.Int64Counter("requests")
counter.Add(ctx, 1,
    metric.WithAttributes(attribute.String("method", "GET")),
)
```

### Benefits of OTel Mode

1. **Standard API**: Use the official OpenTelemetry Metrics API
2. **Ecosystem Compatibility**: Works with OTel tooling and libraries
3. **Future-Proof**: Aligned with industry standards
4. **Instrumentation Libraries**: Can use OTel auto-instrumentation

### When to Use Each Mode

**Use Legacy Mode when:**
- You want the simplest possible API
- You're building a new service from scratch
- You don't need OTel ecosystem integration

**Use OTel Mode when:**
- You need OTel SDK compliance
- You want to use OTel auto-instrumentation libraries
- You're migrating from another OTel-compliant library
- You need to integrate with OTel Collector

## Performance Considerations

### OTel Mode Overhead

OTel mode adds minimal overhead:
- **Attribute conversion**: ~10-20ns per metric (iterator over attribute.Set)
- **Interface indirection**: Negligible (embedded types)
- **No aggregation**: Metrics go directly to pipeline

### Benchmarks

```
BenchmarkLegacyMode/Counter-8     50000000    25.3 ns/op    0 B/op    0 allocs/op
BenchmarkOTelMode/Counter-8       45000000    28.7 ns/op    0 B/op    0 allocs/op
```

**Overhead: ~13% (3.4ns per operation)**

This is well within our performance targets and is primarily due to attribute conversion.

## Configuration

### Provider Options

```go
provider, _ := otel.NewMeterProvider(
    // Set resource information
    otel.WithResource(resource.NewWithAttributes(
        semconv.SchemaURL,
        semconv.ServiceName("my-service"),
        semconv.ServiceVersion("1.0.0"),
    )),
    
    // Pass stats client options
    otel.WithStatsOptions(
        stats.WithServiceName("my-service"),
        stats.WithBufferSize(16384),
        stats.WithWorkers(4),
        stats.WithDropStrategy(stats.DropOldest),
        stats.WithAdaptiveBatching(true),
        
        // Enable backends
        stats.WithDatadog(&stats.DatadogConfig{
            AgentHost: "localhost",
            AgentPort: 8125,
        }),
    ),
)
```

## Limitations

### Current Limitations

1. **No Async Instruments**: Observable instruments not yet implemented
2. **No Views**: Metric views for cardinality control not yet implemented
3. **No Readers**: Custom metric readers not yet supported
4. **Delta Temporality Only**: Only delta aggregation is supported (matches StatsD semantics)

### Planned Features

- [ ] Async/Observable instruments
- [ ] Metric views for cardinality control
- [ ] Custom metric readers
- [ ] Cumulative temporality support
- [ ] Native OTLP exporter

## Examples

See [`examples/otel/main.go`](../examples/otel/main.go) for a complete working example.

## Comparison with Other Libraries

### vs. Official OTel SDK

| Feature | This Library | Official SDK |
|---------|--------------|--------------|
| Performance | Very High (lock-free) | Good |
| Memory Usage | Low (bounded) | Unbounded |
| Blocking | Never blocks | Can block |
| Backends | UDP (Datadog, Prom, CW) | OTLP, Prometheus |
| Aggregation | None (pass-through) | Full aggregation |
| Views | Not yet | Yes |

### vs. StatsD Libraries

| Feature | This Library (OTel) | go-statsd |
|---------|---------------------|-----------|
| API | OTel standard | Custom |
| Type Safety | Strong | Weak |
| Attributes | Structured | Tags (strings) |
| Ecosystem | OTel compatible | Standalone |

## Troubleshooting

### Metrics Not Appearing

1. **Check backend configuration**: Ensure Datadog/Prometheus/CloudWatch agent is running
2. **Check buffer size**: Increase `WithBufferSize()` if dropping metrics
3. **Check flush interval**: Metrics are batched, wait for flush
4. **Enable logging**: Use `stats.WithDebug(true)` to see internal errors

### Performance Issues

1. **Too many attributes**: Limit cardinality to avoid overwhelming backends
2. **Buffer too small**: Increase `WithBufferSize()`
3. **Not enough workers**: Increase `WithWorkers()`
4. **Enable adaptive batching**: Use `WithAdaptiveBatching(true)`

## Contributing

See [CLAUDE.md](../CLAUDE.md) for development guidelines.

## License

Copyright © 2024 Convoy Road Trips App
