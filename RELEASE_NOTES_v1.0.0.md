# Stats v1.0.0 - First Public Release

**A production-ready, non-blocking UDP stats library for Go with full OpenTelemetry SDK compliance.**

We're excited to announce the first public release of the Stats library - a high-performance metrics collection system designed for modern Go applications. Built from the ground up with non-blocking architecture and dual-mode operation, Stats delivers exceptional performance without compromising on standards compliance.

## What's New

🎯 **Dual-Mode API** - Choose your style: simple legacy API or standards-based OpenTelemetry API - both share the same high-performance pipeline

🚀 **Lock-Free Performance** - Non-blocking ring buffer with atomic operations ensures your application never waits. Achieve >100k ops/sec with <100ns latency

🔌 **Multi-Backend Support** - Export to Datadog (DogStatsD), Prometheus (StatsD), and CloudWatch (EMF) simultaneously with isolated failure domains

📊 **OpenTelemetry Compliant** - Full OTel SDK implementation with all synchronous instruments (Counter, Histogram, Gauge, UpDownCounter)

🛡️ **Production-Hardened** - Circuit breakers, panic recovery, adaptive batching, graceful degradation, and memory-bounded operation

⚡ **Zero Allocation** - Object pooling in hot paths minimizes GC pressure for maximum throughput

## Getting Started

```bash
go get github.com/convoy-road-trips-app/stats
```

See [README.md](README.md) for quick start guide and examples.

## Performance Highlights

| Metric | Result |
|--------|--------|
| Push latency (p99) | ~25ns |
| Throughput | >200k/sec |
| Memory usage | ~5MB |
| Drop rate | <0.1% |

## Documentation

- [README.md](README.md) - Quick start and API overview
- [docs/architecture.md](docs/architecture.md) - System architecture and design
- [docs/otel_compliance.md](docs/otel_compliance.md) - OpenTelemetry compliance details
- [docs/performance_guide.md](docs/performance_guide.md) - Performance tuning guide

## What's Next?

Check out the [examples/](examples/) directory for working code samples demonstrating both API modes and multi-backend configurations.

---

**Note**: This is the first public release. The library has been designed with production-readiness in mind, featuring comprehensive testing (unit, integration, race detector) and extensive documentation. We're committed to API stability and backward compatibility going forward.
