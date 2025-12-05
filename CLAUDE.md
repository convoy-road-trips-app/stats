# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

A production-ready, non-blocking UDP stats library for the Convoy Road Trips App with **full OpenTelemetry SDK compliance**. Designed for high-throughput metric collection with multi-backend support (CloudWatch, Prometheus, Datadog, OTLP), robust error handling, and adaptive backpressure management.

**Module Path:** `github.com/convoy-road-trips-app/stats`
**Go Version:** 1.25.5

### Dual-Mode Operation

The library supports two API modes that share the same high-performance pipeline:

1. **Legacy Mode** (`stats.NewClient()`): Simple, high-performance API with convenience methods
2. **OTel Mode** (`otel.NewMeterProvider()`): Full OpenTelemetry SDK compliance with standard OTel instruments

Both modes use identical underlying components (ring buffer, worker pool, exporters), ensuring consistent performance characteristics.

## Architecture

For detailed architecture documentation, see [docs/architecture.md](docs/architecture.md).

### Core Components

1. **Lock-Free Ring Buffer** (`transport/buffer.go`)
   - 16K capacity by default, power-of-2 sizing for optimal performance
   - Uses atomic CAS operations for lock-free concurrent access
   - Cache line padding to avoid false sharing
   - Target: 20ns per operation, >10M ops/sec

2. **UDP Connection Pool** (`transport/udp.go`)
   - Pre-allocated connection pool (4 connections default)
   - 1MB write buffer per connection
   - Non-blocking sends with write deadlines
   - Isolated failure domains per backend

3. **Circuit Breaker** (`transport/circuit.go`)
   - Prevents cascading failures
   - States: Closed, Open, Half-Open
   - Configurable threshold and timeout

4. **Metric Pipeline** (`pipeline.go`)
   - Worker pool pattern for async processing
   - **Parallel Exporting**: Exporters run concurrently (bulkheading)
   - **Panic Recovery**: Worker goroutines protected from exporter panics
   - **Adaptive Batching**: Dynamically adjusts batch size based on buffer pressure
   - Memory-aware buffering
   - Graceful shutdown with context cancellation

5. **Public API**
   - **Legacy API** (`client.go`): Counter, Gauge, Histogram with convenience methods
   - **OTel API** (`otel/`): Full OpenTelemetry SDK implementation with MeterProvider, Meter, and all synchronous instruments
   - Builder pattern for complex metrics
   - Object pooling for zero allocations

### Data Flow

```
Application Code
    ├─ Legacy Mode: stats.NewClient() → Counter/Gauge/Histogram
    └─ OTel Mode: otel.NewMeterProvider() → Int64Counter/Float64Histogram/etc.
    ↓
Ring Buffer (non-blocking push, drop strategies)
    ↓
Worker Pool (4 workers, adaptive batching)
    ↓
Exporters (parallel, isolated)
    ↓
UDP Connection Pool / OTLP HTTP
    ↓
Backend Agents (Datadog/Prometheus/CloudWatch/OTLP Collector)
```

## Development Commands

### Using Makefile (Recommended)

```bash
# Run all tests
make test

# Run tests with race detector
make test-race

# Run tests with coverage report
make test-cover

# Run benchmarks
make bench

# Run linters
make lint

# Build the library
make build

# Clean artifacts
make clean

# Show all available targets
make help
```

### Manual Commands

```bash
# Run all tests
go test ./...

# Run tests with coverage
go test -cover ./...

# Run tests with race detector
go test -race ./...

# Run benchmarks
go test -bench=. ./transport/...

# Run specific package tests
go test ./transport/...
```

### Running Examples
```bash
# Basic legacy API example
cd examples/basic && go run main.go

# OpenTelemetry API example
cd examples/otel && go run main.go

# Multi-backend example
cd examples/multibackend && go run main.go
```

### Code Quality
```bash
# Format code
go fmt ./...

# Run static analysis
go vet ./...

# Run linter (if golangci-lint is installed)
golangci-lint run
```

## Performance Targets

| Metric | Target | Excellent |
|--------|--------|-----------|
| Push latency (p99) | <100ns | <50ns |
| End-to-end (p99) | <100ms | <50ms |
| Throughput | >100k/sec | >500k/sec |
| Memory usage | <10MB | <5MB |
| Drop rate | <1% | <0.1% |

## Key Design Decisions

1. **Non-Blocking Guarantee**: Ring buffer with configurable drop strategies ensures application never blocks
2. **Lock-Free Design**: Atomic operations and CAS for zero contention
3. **Object Pooling**: `sync.Pool` for metrics and buffers to minimize GC pressure
4. **Multiple Backends**: Independent exporters with isolated failure domains
5. **Memory Bounds**: Configurable limits prevent unbounded growth
6. **Graceful Degradation**: Drops metrics under pressure rather than failing
7. **Parallel Exporting**: Slow backends don't block fast ones
8. **Adaptive Backpressure**: Automatically adjusts processing rate based on buffer utilization

## Robustness Features

### Drop Strategies
Configure how the system handles buffer overflow:
- `DropNewest` (default): Rejects incoming metrics when buffer is full
- `DropOldest`: Removes oldest metric to make room for new ones

```go
client, err := stats.NewClient(
    stats.WithDropStrategy(stats.DropOldest),
)
```

### Adaptive Batching
Automatically increases batch size when buffer utilization exceeds 50%:
```go
client, err := stats.NewClient(
    stats.WithAdaptiveBatching(true),
)
```

### Error Tracking
Per-exporter error tracking for visibility:
```go
stats := client.Stats()
for name, count := range stats.Pipeline.ExporterErrors {
    fmt.Printf("Exporter %s: %d errors\n", name, count)
}
```

## File Structure

```
.
├── client.go              # Legacy API (Counter, Gauge, Histogram)
├── pipeline.go            # Metric processing pipeline (shared by both modes)
├── metric.go              # Metric types and pooling
├── config.go              # Configuration structures
├── options.go             # Functional options
├── errors.go              # Error definitions
├── Makefile              # Build and test automation
├── otel/                  # OpenTelemetry SDK implementation
│   ├── meter_provider.go  # MeterProvider (creates Meters)
│   ├── meter.go           # Meter (creates instruments)
│   └── instruments.go     # All OTel instruments (Counter, Histogram, Gauge, etc.)
├── transport/
│   ├── buffer.go          # Lock-free ring buffer
│   ├── udp.go            # UDP connection pool
│   └── circuit.go        # Circuit breaker
├── exporters/
│   ├── datadog/          # Datadog DogStatsD exporter
│   ├── prometheus/       # Prometheus StatsD exporter
│   ├── cloudwatch/       # CloudWatch EMF exporter
│   └── otlp/             # OTLP HTTP exporter
├── serializers/
│   ├── dogstatsd.go      # DogStatsD format
│   ├── statsd.go         # StatsD format
│   └── emf.go            # CloudWatch EMF format
├── models/               # Shared data structures
│   ├── metric.go         # Core metric model
│   ├── config.go         # Config models
│   └── otlp_config.go    # OTLP-specific config
├── internal/
│   ├── types/            # Internal type definitions
│   └── pool/             # Object pooling
├── examples/
│   ├── basic/            # Legacy API usage
│   ├── otel/             # OpenTelemetry API usage
│   └── multibackend/     # Multiple backends
├── docs/                 # Detailed documentation
│   ├── architecture.md
│   ├── otel_compliance.md      # OTel feature documentation
│   ├── performance_guide.md
│   ├── performance_checklist.md
│   ├── benchmarking.md
│   └── performance_patterns.md
└── CLAUDE.md             # This file
```

## Usage Examples

### Legacy API

```go
client, err := stats.NewClient(
    stats.WithServiceName("my-service"),
    stats.WithEnvironment("production"),
    stats.WithBufferSize(16384),
    stats.WithWorkers(4),

    // Backpressure handling
    stats.WithDropStrategy(stats.DropOldest),
    stats.WithAdaptiveBatching(true),

    // Backend configuration
    stats.WithDatadog(&stats.DatadogConfig{
        AgentHost: "localhost",
        AgentPort: 8125,
    }),
)
defer client.Close()

// Record metrics (never blocks!)
client.Counter("http.requests", 1.0,
    stats.WithAttribute("method", "GET"),
    stats.WithAttribute("status", "200"),
)

client.Timing("db.query", duration,
    stats.WithAttribute("table", "users"),
)
```

### OpenTelemetry API

```go
import (
    "go.opentelemetry.io/otel/attribute"
    "go.opentelemetry.io/otel/metric"
    "github.com/convoy-road-trips-app/stats/otel"
)

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
defer provider.Shutdown(context.Background())

// Use standard OTel API
meter := provider.Meter("my-app")
counter, _ := meter.Int64Counter("http.requests")
histogram, _ := meter.Float64Histogram("db.query.duration")

// Record metrics with OTel API
ctx := context.Background()
counter.Add(ctx, 1, metric.WithAttributes(
    attribute.String("method", "GET"),
    attribute.String("status", "200"),
))

histogram.Record(ctx, duration.Seconds(), metric.WithAttributes(
    attribute.String("table", "users"),
))
```

## Testing

- **Unit tests**: Core components (buffer, pool, circuit breaker, pipeline)
- **OTel compliance tests**: MeterProvider, instruments, attribute conversion
- **Robustness tests**: Panic recovery, parallel exporting, error tracking
- **Backpressure tests**: Drop strategies, adaptive batching
- **Concurrent tests**: Race detector enabled
- **Benchmarks**: Performance validation for both legacy and OTel modes

Run tests with: `make test` or `make test-race`

### Running Specific Tests

```bash
# Test specific package
go test ./otel/...

# Test with verbose output
go test -v ./transport/...

# Run specific test
go test -run TestRingBuffer ./transport/...
```

## Documentation

- **README.md**: Quick start and API overview (both legacy and OTel modes)
- **docs/architecture.md**: Detailed system architecture
- **docs/otel_compliance.md**: OpenTelemetry SDK compliance details, supported instruments, limitations
- **docs/performance_guide.md**: Performance tuning and optimization
- **docs/performance_checklist.md**: Pre-deployment performance validation
- **docs/benchmarking.md**: Benchmarking methodology
- **docs/performance_patterns.md**: Common performance patterns

## Key Implementation Details

### OpenTelemetry Integration

The `otel/` package provides a complete OpenTelemetry SDK implementation:

- **MeterProvider** (`otel/meter_provider.go`): Top-level provider that creates Meters
- **Meter** (`otel/meter.go`): Creates and manages instruments
- **Instruments** (`otel/instruments.go`): All synchronous instruments (Counter, Histogram, Gauge, UpDownCounter)
- **Attribute Conversion**: Automatic conversion between OTel attributes and stats attributes

**Important**: Asynchronous/Observable instruments are NOT supported due to OTel SDK limitations (unexported marker methods). See `docs/otel_compliance.md` for details.

### Backend Exporters

Four exporters are available, each with isolated failure domains:

1. **Datadog** (`exporters/datadog/`): DogStatsD protocol over UDP
2. **Prometheus** (`exporters/prometheus/`): StatsD protocol over UDP
3. **CloudWatch** (`exporters/cloudwatch/`): EMF format via CloudWatch Logs
4. **OTLP** (`exporters/otlp/`): OpenTelemetry Protocol over HTTP

### Performance Characteristics

- **Legacy Mode**: ~25ns per metric push (p99)
- **OTel Mode**: ~29ns per metric push (p99) - only +13% overhead
- Both modes share the same pipeline, so end-to-end latency is identical
