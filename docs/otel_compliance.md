# OpenTelemetry Compliance

This document describes the OpenTelemetry (OTel) compliance features of the stats library.

## Overview

The stats library provides **dual-mode operation**:

1. **Legacy Mode** (default): Simple, high-performance API (`stats.NewClient()`)
2. **OTel Mode**: An implementation of the OpenTelemetry Metrics API (`otel.NewMeterProvider()`)

Both modes share the same high-performance pipeline underneath, ensuring consistent performance characteristics.

OTel Mode implements the Metrics **API**; it is not the OpenTelemetry SDK. It has no views or custom readers, and a few instruments map onto the pipeline's counter/gauge/histogram types with documented divergences (see [Limitations](#limitations)).

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

All synchronous instruments can be created and recorded:

| Instrument | Description | Use Case |
|------------|-------------|----------|
| `Int64Counter` | Monotonically increasing integer | Request counts, bytes sent |
| `Float64Counter` | Monotonically increasing float | Fractional increments |
| `Int64UpDownCounter` | Can increase or decrease (exported as a gauge, see [Limitations](#limitations)) | Active connections, queue size |
| `Float64UpDownCounter` | Can increase or decrease (exported as a gauge, see [Limitations](#limitations)) | Temperature changes |
| `Int64Histogram` | Distribution of integer values | Response sizes |
| `Float64Histogram` | Distribution of float values | Request durations, latencies |
| `Int64Gauge` | Point-in-time integer value | CPU usage, memory |
| `Float64Gauge` | Point-in-time float value | CPU percentage, ratios |

### Asynchronous (Observable) Instruments

All six observable instruments are supported since v1.1.0: `Int64/Float64ObservableCounter`, `Int64/Float64ObservableUpDownCounter` and `Int64/Float64ObservableGauge`, registered through instrument callbacks or `Meter.RegisterCallback`.

- Callbacks run on a collection loop that starts with the first registration and runs every `otel.WithCollectionInterval` (default 10s). `MeterProvider.ForceFlush` and `Shutdown` run one more collection with the caller's context before flushing.
- ObservableCounter callbacks report cumulative totals. The provider records the increase since the last observation of each series, so the exported cumulative value equals the observed total.
- ObservableUpDownCounter and ObservableGauge observations are exported as gauges.
- Registration errors: `ErrNilCallback`, `ErrForeignObservable` (instrument from another implementation), `ErrObservableMeter` (instrument from another meter, skipped), `ErrUnregisteredObservable` (observing an instrument the callback was not registered for; the observation is dropped). Collection errors go to `otel.Handle`.
- Callbacks run with an empty span context and never produce exemplars.

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
│  • OTLP (gRPC)                                                │
└───────────────────────────────────────────────────────────────┘
```

### Key Design Decisions

1. **No Aggregation in the Pipeline**: Instruments pass observations directly to the underlying stats client, which buffers, batches and exports them. Aggregation happens only inside the OTLP exporter (see [OTLP Export Semantics](#otlp-export-semantics)); the Datadog, Prometheus (StatsD) and CloudWatch EMF exporters keep their per-observation behavior.

2. **Attribute Conversion**: OTel `attribute.Set` is converted to stats `MetricOption` format using an iterator to avoid allocations.

3. **Embedded Types**: We use `embedded.Meter`, `embedded.Int64Counter`, etc. to satisfy OTel SDK interfaces without implementing marker methods.

4. **Shared Pipeline**: Both modes use the same lock-free ring buffer and worker pool, ensuring consistent performance.

## OTLP Export Semantics

These rules apply to the OTLP exporter (`stats.WithOTLP`) in both modes.

### Temporality

- Sums (counters) and histograms are exported with **cumulative** temporality by default. `stats.WithTemporality(stats.Delta)` opts into delta.
- Cumulative state is kept per (metric name, attribute set) inside the exporter. A failed export still advances the state, so the next cumulative point includes the interval the collector did not receive.
- Workers export batches concurrently, so a later export can carry observations older than the previous point of the same series. Cumulative points are therefore stamped at least 1 ms after the previous point of the series. Without this, Prometheus drops the larger total as a duplicate sample.
- A delta point starts where the previous point of the series ended. When a late export holds only older observations, its `Time` is raised to that start, so `StartTime <= Time` always holds; delta points get no 1 ms spacing.
- A sum point merged from several observations of one batch is stamped at the newest of them, whatever their order in the batch.
- Prometheus' OTLP receiver (used by `grafana/otel-lgtm`) ingests only cumulative sums and histograms; delta series do not reach the query surface there.

### Histograms

- Observations are aggregated per export batch into explicit-bucket histograms, one data point per attribute set, with count, sum, min, max and cumulative-range bucket counts.
- Default bounds are the telemetry spec D9 seconds buckets: `0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10` (`models.DefaultHistogramBuckets()`).
- `stats.WithHistogramBuckets(bounds)` overrides them. Bounds must be finite and strictly increasing; an empty list is rejected.
- In Prometheus these arrive as `<name>_bucket{le="..."}`, `<name>_count` and `<name>_sum`. `test/integration/lgtm/buckets_test.go` checks every `le` series and its count against the LGTM stack for the legacy API, a custom bucket override, and the OTel API.

### Resource

`service.name`, `deployment.environment` and `service.version` resolve in this order (later wins): `OTEL_RESOURCE_ATTRIBUTES` < `OTEL_SERVICE_NAME` / `DEPLOYMENT_ENVIRONMENT` / `SERVICE_VERSION` < explicit options (`WithServiceName`, `WithEnvironment`, `WithOTLPResourceAttributes`, `otel.WithResource`). Missing identity falls back to `unknown_service` / `unknown`.

The resource schema URL is exported as `ResourceMetrics.schema_url`. It comes from the `otel.WithResource` resource (for example `resource.NewWithAttributes(semconv.SchemaURL, ...)`) or from `stats.WithOTLPResourceSchemaURL`; it is empty by default.

### Metric Metadata

`Description` and `Unit` are exported on the OTLP metric for observable instruments (`metric.WithDescription` / `metric.WithUnit`) and for legacy observations recorded with `stats.WithDescription` / `stats.WithUnit`. Synchronous OTel instruments do not export them yet (see [Limitations](#limitations)).

### Attributes and Cardinality (all exporters)

Enforced in the shared pipeline, so Datadog, Prometheus (StatsD) and CloudWatch EMF receive the same sanitized attributes:

- Attribute keys must be one or more identifier segments joined by single dots: `^[a-zA-Z_][a-zA-Z0-9_]*(\.[a-zA-Z_][a-zA-Z0-9_]*)*$`. OTel semantic-convention keys such as `http.method`, `http.route` and `http.response.status_code` are accepted and exported unchanged; keys are never rewritten. An observation with any other key (`bad..key`, `.key`, `key.`, `http.1x`, `http-method`, non-ASCII) is rejected with `stats.ErrInvalidTagKey`: nothing is recorded, no series slot is used and no drop is counted. This is stricter than the OTel specification, which allows any non-empty key, so semantic-convention templates with free-form segments (for example `http.request.header.content-type`) are rejected. Metric names are not checked by this rule.
- Prometheus' OTLP translation maps both `http.method` and `http_method` to the label `http_method`; do not send both spellings on one metric. The Prometheus StatsD exporter writes each attribute into the dotted metric path as `key_value`, so a dotted key adds path segments, as in v1.0.1.
- String values are capped at 256 runes. Only the first 10 keys in lexical order are kept.
- Each metric name admits at most 2000 distinct attribute sets by default (`stats.WithMaxCardinality`). Observations of new series beyond the limit return `stats.ErrCardinalityLimit`.
- Drops are counted in `telemetry_dropped_labels_total{reason="label_limit"|"series_limit"}`.
- OTel instruments discard these errors (the API has no error return), so rejected observations are silently not recorded in OTel Mode.

### Exemplars

When the recording context holds a valid, **sampled** span, counter and histogram observations carry its `trace_id`/`span_id`, and the OTLP exporter emits them as exemplars: the latest per bucket for histograms and the latest per series for sums. Gauges and unsampled or span-less contexts get none, and exemplars never repeat into later cumulative exports.

### Flush, Shutdown and Retry

- `Client.Flush(ctx)` / `MeterProvider.ForceFlush(ctx)` export every buffered observation with the caller's context. `Shutdown(ctx)` drains the buffer before returning and returns `context.DeadlineExceeded` (wrapped) when ctx ends first. Both report failures of a background export that was already in flight when they were called.
- `stats.WithOTLPRetry(initial, maxInterval, maxElapsed)` enables exponential-backoff retries of retryable OTLP failures. Retries are bounded by the export context.
- Background exports (ticker or full batch) are bounded by `WithUDPTimeout` (default 100 ms), including OTLP exports. Raise it for remote collectors; otherwise slow background exports fail and are reported by the next Flush/Shutdown.

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
        stats.WithOTLP(&stats.OTLPConfig{
            Endpoint:    "localhost:4317",
            Insecure:    true,
            ServiceName: "my-service",
        }),
    ),
)
```

## Limitations

### Current Limitations

1. **UpDownCounter is a gauge**: synchronous `UpDownCounter.Add(n)` records a gauge whose value is `n`, the latest increment, not a running total. Observable UpDownCounters export the observed value as a gauge. Neither is exported as a non-monotonic OTLP Sum.
2. **No Views**: Metric views are not implemented; cardinality is bounded by the fixed limits above.
3. **No Readers**: Custom metric readers are not supported; export is push-only through the pipeline.
4. **Units are not converted**: `Client.Timing` records milliseconds into a histogram, while the default buckets are in seconds. Use `Histogram` with seconds, or set `WithHistogramBuckets`.
5. **Counters accept negative values** in the legacy API (`Client.Counter`); they are not rejected.
6. **No OTLP environment configuration**: `OTEL_EXPORTER_OTLP_ENDPOINT` and related variables are not read; configure the endpoint with `WithOTLP`.
7. **Prometheus OTLP ingestion requires cumulative temporality** (the default); `WithTemporality(stats.Delta)` series are dropped by Prometheus' OTLP receiver.
8. **Synchronous instruments drop description and unit**: `metric.WithDescription` / `metric.WithUnit` on synchronous OTel instruments are accepted but not exported; observable instruments export them.

### Planned Features

- [x] Async/Observable instruments (v1.1.0)
- [x] Cumulative temporality support (v1.1.0, default)
- [ ] Non-monotonic Sum export for UpDownCounters
- [ ] Metric views for cardinality control
- [ ] Custom metric readers

## Examples

See [`examples/otel/main.go`](../examples/otel/main.go) for a complete working example.

## Comparison with Other Libraries

### vs. Official OTel SDK

| Feature | This Library | Official SDK |
|---------|--------------|--------------|
| Performance | Very High (lock-free) | Good |
| Memory Usage | Low (bounded) | Unbounded |
| Blocking | Never blocks | Can block |
| Backends | Datadog, Prom, CW, OTLP | OTLP, Prometheus |
| Aggregation | Per-batch histograms + cumulative state in the OTLP exporter only | Full aggregation |
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
3. **Check flush interval**: Metrics are batched; call `Flush(ctx)` / `ForceFlush(ctx)` or wait for the flush interval
4. **Inspect pipeline stats**: `client.Stats().Pipeline` reports processed, dropped and per-exporter error counts
5. **Check attribute keys**: keys with empty segments (`bad..key`), digit-led segments or characters outside `[A-Za-z0-9_.]` are rejected (`ErrInvalidTagKey`); OTel instruments drop those observations silently. Dotted keys such as `http.method` are valid
6. **Prometheus via OTLP**: keep the default cumulative temporality; delta series are not ingested

### Performance Issues

1. **Too many attributes**: Limit cardinality to avoid overwhelming backends
2. **Buffer too small**: Increase `WithBufferSize()`
3. **Not enough workers**: Increase `WithWorkers()`
4. **Enable adaptive batching**: Use `WithAdaptiveBatching(true)`

## Contributing

See [CLAUDE.md](../CLAUDE.md) for development guidelines.

## License

Copyright © 2024 Convoy Road Trips App
