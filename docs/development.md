# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

An OpenTelemetry-based, non-blocking UDP stats library for the Convoy Road Trips App. Designed for high-throughput metric collection with support for multiple backends (CloudWatch, Prometheus, Datadog).

**Module Path:** `github.com/convoy-road-trips-app/stats`
**Go Version:** 1.25.5

## Architecture

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
   - Adaptive batching (up to 100 metrics, 100ms max age)
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
Ring Buffer (non-blocking push)
    ↓
Worker Pool (4 workers, batching)
    ↓
Exporters (per backend)
    ↓
UDP Connection Pool
    ↓
Backend Agents (CloudWatch/Prometheus/Datadog)
```

## Development Commands

### Building
```bash
go build ./...
```

### Running Tests
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

### Dependencies
```bash
# Add a new dependency
go get package-name

# Tidy up dependencies
go mod tidy

# Verify dependencies
go mod verify
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

1. **Non-Blocking Guarantee**: Ring buffer with drop-on-full policy ensures application never blocks
2. **Lock-Free Design**: Atomic operations and CAS for zero contention
3. **Object Pooling**: `sync.Pool` for metrics and buffers to minimize GC pressure
4. **Multiple Backends**: Independent exporters with isolated failure domains
5. **Memory Bounds**: Configurable limits prevent unbounded growth
6. **Graceful Degradation**: Drops metrics under pressure rather than failing

## File Structure

```
.
├── client.go              # Public API
├── pipeline.go            # Metric processing pipeline
├── metric.go              # Metric types and pooling
├── config.go              # Configuration structures
├── options.go             # Functional options
├── errors.go              # Error definitions
├── transport/
│   ├── buffer.go          # Lock-free ring buffer
│   ├── udp.go            # UDP connection pool
│   └── circuit.go        # Circuit breaker
├── examples/
│   └── basic/main.go     # Basic usage example
└── CLAUDE.md             # This file
```

## Usage Example

```go
client, err := stats.NewClient(
    stats.WithServiceName("my-service"),
    stats.WithEnvironment("production"),
    stats.WithBufferSize(16384),
    stats.WithWorkers(4),
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

## Next Steps (Phase 2+)

1. Implement backend exporters:
   - Datadog DogStatsD serializer
   - Prometheus StatsD serializer
   - CloudWatch EMF serializer

2. Add OpenTelemetry integration:
   - MeterProvider setup
   - PeriodicReaders per backend
   - Views for cardinality management

3. Production hardening:
   - String interning for tags
   - Binary serialization
   - UDP packet packing
   - Internal metrics collection

## Testing

- **Unit tests**: Core components (buffer, pool, circuit breaker)
- **Concurrent tests**: Race detector enabled
- **Benchmarks**: Performance validation
- **Integration tests**: End-to-end with mock backends (TODO)

Run tests with: `go test ./... -race -cover`
