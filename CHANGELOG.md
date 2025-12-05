# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.0] - 2025-12-05

### Added

**First public release** - Production-ready, high-performance stats library with OpenTelemetry compliance.

#### Core Features
- **Dual-Mode API**: Simple legacy API (`stats.NewClient()`) and OpenTelemetry-compliant API (`otel.NewMeterProvider()`)
- **Multi-Backend Support**: Export to Datadog (DogStatsD), Prometheus (StatsD), and CloudWatch (EMF)
- **Lock-Free Architecture**: Non-blocking ring buffer with atomic operations - your application never waits
- **High Performance**: >100k ops/sec throughput, <100ns push latency (p99)
- **Zero Allocation**: Object pooling in hot paths minimizes GC pressure

#### Robustness
- Circuit breakers with isolated failure domains per backend
- Panic recovery in exporters - worker pool continues processing
- Graceful degradation with configurable drop strategies (DropNewest, DropOldest)
- Adaptive batching under high load
- Memory-bounded buffering with configurable limits

#### OpenTelemetry Integration
- Full synchronous instrument support: Counter, UpDownCounter, Histogram, Gauge (Int64/Float64)
- Standard OTel Metrics API compatibility
- Attribute conversion between OTel and internal formats
- Shared high-performance pipeline for both API modes
- See [docs/otel_compliance.md](docs/otel_compliance.md) for details

#### Configuration & Observability
- Comprehensive configuration via functional options
- Pipeline statistics: processed, dropped, errors, buffer utilization
- Per-exporter error tracking
- Rate limiting with token bucket algorithm

#### Documentation
- Complete API documentation and examples
- Architecture guide: [docs/architecture.md](docs/architecture.md)
- Performance tuning guide: [docs/performance_guide.md](docs/performance_guide.md)
- OpenTelemetry compliance: [docs/otel_compliance.md](docs/otel_compliance.md)

### Performance Benchmarks

| Metric | Target | Achieved |
|--------|--------|----------|
| Push latency (p99) | <100ns | ~25ns |
| Throughput | >100k/sec | >200k/sec |
| Memory usage | <10MB | ~5MB |
| OTel mode overhead | - | +13% (~3ns) |

See [README.md](README.md) for installation and quick start guide.

[1.0.0]: https://github.com/convoy-road-trips-app/stats/releases/tag/v1.0.0
