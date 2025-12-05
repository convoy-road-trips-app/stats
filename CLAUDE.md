# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

A production-ready, non-blocking UDP stats library for the Convoy Road Trips App. Designed for high-throughput metric collection with multi-backend support (CloudWatch, Prometheus, Datadog), robust error handling, and adaptive backpressure management.

**Module Path:** `github.com/convoy-road-trips-app/stats`
**Go Version:** 1.25.5

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

5. **Public API** (`client.go`)
   - Simple API: Counter, Gauge, Histogram
   - Convenience methods: Increment, Timing
   - Builder pattern for complex metrics
   - Object pooling for zero allocations

### Data Flow

```
Application → Client.Counter/Gauge/Histogram
    ↓
Ring Buffer (non-blocking push, drop strategies)
    ↓
Worker Pool (4 workers, adaptive batching)
    ↓
Exporters (parallel, isolated)
    ↓
UDP Connection Pool
    ↓
Backend Agents (CloudWatch/Prometheus/Datadog)
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
# Basic example
cd examples/basic && go run main.go

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
├── client.go              # Public API
├── pipeline.go            # Metric processing pipeline
├── metric.go              # Metric types and pooling
├── config.go              # Configuration structures
├── options.go             # Functional options
├── errors.go              # Error definitions
├── Makefile              # Build and test automation
├── transport/
│   ├── buffer.go          # Lock-free ring buffer
│   ├── udp.go            # UDP connection pool
│   └── circuit.go        # Circuit breaker
├── exporters/
│   ├── datadog/          # Datadog exporter
│   ├── prometheus/       # Prometheus exporter
│   └── cloudwatch/       # CloudWatch exporter
├── serializers/
│   ├── dogstatsd.go      # DogStatsD format
│   ├── statsd.go         # StatsD format
│   └── emf.go            # CloudWatch EMF format
├── examples/
│   ├── basic/            # Basic usage example
│   └── multibackend/     # Multi-backend example
├── docs/                 # Detailed documentation
│   ├── architecture.md
│   ├── performance_guide.md
│   ├── benchmarking.md
│   └── performance_patterns.md
└── CLAUDE.md             # This file
```

## Usage Example

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

## Testing

- **Unit tests**: Core components (buffer, pool, circuit breaker, pipeline)
- **Robustness tests**: Panic recovery, parallel exporting, error tracking
- **Backpressure tests**: Drop strategies, adaptive batching
- **Concurrent tests**: Race detector enabled
- **Benchmarks**: Performance validation

Run tests with: `make test` or `make test-race`

## Documentation

- **README.md**: Quick start and API overview
- **docs/architecture.md**: Detailed system architecture
- **docs/performance_guide.md**: Performance tuning and optimization
- **docs/benchmarking.md**: Benchmarking methodology
- **docs/performance_patterns.md**: Common performance patterns

## Recent Enhancements

1. **Parallel Exporting**: Exporters now run concurrently to prevent slow backends from blocking others
2. **Panic Recovery**: Worker goroutines are protected from panics in exporters
3. **Per-Exporter Error Tracking**: Detailed visibility into which backends are failing
4. **Drop Strategies**: Configurable behavior for handling buffer overflow
5. **Adaptive Batching**: Automatic adjustment of batch size based on buffer pressure
