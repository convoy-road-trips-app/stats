# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.1.0] - 2026-10-01

OpenTelemetry conformance release. See [docs/otel_compliance.md](docs/otel_compliance.md) for the full semantics and limitations.

### Added

- **OTLP explicit-bucket histograms**: observations are aggregated per attribute set with count, sum, min, max and bucket counts. Default bounds are the D9 seconds buckets (`0.005 … 10`); `WithHistogramBuckets` overrides them (finite, strictly increasing).
- **Temporality control**: `WithTemporality(stats.Cumulative | stats.Delta)`. A delta point starts where the previous point of its series ended and never ends before it starts, even when a worker exports older observations late.
- **Full OTLP resource**: `service.name`, `deployment.environment` and `service.version` from options, `OTEL_SERVICE_NAME` / `DEPLOYMENT_ENVIRONMENT` / `SERVICE_VERSION`, `OTEL_RESOURCE_ATTRIBUTES`, `WithOTLPResourceAttributes` and `otel.WithResource`. The resource schema URL is exported as `ResourceMetrics.schema_url`, from `otel.WithResource` or `WithOTLPResourceSchemaURL`.
- **Cardinality limits**: at most 10 attributes per observation, 256-rune values and 2000 series per metric (`WithMaxCardinality`), with `ErrCardinalityLimit` and the `telemetry_dropped_labels_total{reason}` counter.
- **Reliable flush**: `Client.Flush(ctx)`, `MeterProvider.ForceFlush(ctx)`, a draining `Shutdown(ctx)` and `WithOTLPRetry(initial, maxInterval, maxElapsed)`.
- **Exemplars**: counter and histogram observations under a sampled span carry `trace_id`/`span_id` exemplars in OTLP.
- **Observable instruments**: `Int64/Float64 ObservableCounter`, `ObservableUpDownCounter` and `ObservableGauge`, `Meter.RegisterCallback`, and `otel.WithCollectionInterval` (default 10s).
- **Metric metadata**: `WithDescription` and `WithUnit` metric options; OTLP exports the description and unit of observable instruments and of observations recorded with these options.
- **LGTM CI job**: the `grafana/otel-lgtm` integration tests run in CI again and check histogram `_bucket{le}` series and counts at the Prometheus query API.

### Changed

- **OTLP sums and histograms are cumulative by default** (previously delta). Use `WithTemporality(stats.Delta)` for the old behavior. Prometheus' OTLP receiver ingests only cumulative series.
- **Malformed attribute keys are rejected**: a key must be one or more identifier segments joined by single dots, `^[a-zA-Z_][a-zA-Z0-9_]*(\.[a-zA-Z_][a-zA-Z0-9_]*)*$`. OTel semantic-convention keys such as `http.method` and `http.route` are accepted and exported unchanged. Keys with an empty segment (`bad..key`, `.key`, `key.`), a segment starting with a digit, or any other character (`-`, space, `/`, non-ASCII) return `ErrInvalidTagKey` and nothing is recorded, for every backend; v1.0.1 accepted them. OTel instruments drop such observations silently.
- Datadog, Prometheus (StatsD) and CloudWatch EMF receive the same sanitized and trimmed attributes as OTLP.
- `Client.Shutdown`/`Close` now drain buffered metrics, and Flush/Shutdown report failures of background exports already in flight.

### Fixed

- Cumulative OTLP points of a series are stamped at least 1 ms after the previous point. Concurrent workers could export a larger total with an older or same-millisecond timestamp, which Prometheus dropped as a duplicate sample. A sum point merged from a batch is stamped no earlier than the newest observation it includes.
- With cumulative temporality, a failed OTLP export no longer loses its interval: the next point includes it.
- `RingBuffer.Push` no longer reports a spurious `ErrBufferFull` under concurrent pops.
- `RingBuffer` no longer loses or duplicates an item while full. `Push` never waits for a consumer to release a slot (it reports the buffer full), and `DropOldest` removes the oldest item with the new non-waiting `RingBuffer.TryPop`.

### Known Limitations

- `UpDownCounter` (sync and observable) is exported as a gauge; the sync `Add(n)` gauge value is the latest increment, not a running total.
- Synchronous OTel instruments do not export `WithDescription` / `WithUnit`.
- Values are carried as `float64`: `Int64*` instruments are exported as OTLP double points, exact only up to 2^53.
- `Client.Timing` records milliseconds while the default buckets are in seconds.
- `Client.Counter` accepts negative values.
- `OTEL_EXPORTER_OTLP_ENDPOINT` is not read; no views or custom readers.
- Background exports, OTLP included, are bounded by `WithUDPTimeout` (100 ms default).

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

[1.1.0]: https://github.com/convoy-road-trips-app/stats/compare/v1.0.1...v1.1.0
[1.0.0]: https://github.com/convoy-road-trips-app/stats/releases/tag/v1.0.0
