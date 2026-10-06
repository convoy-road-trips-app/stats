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
- `stats.WithHistogramBucketsFor(name, bounds...)` overrides the bounds for one metric name, with the same validation. Lookup order is the per-name bounds, then the global bounds, then the defaults. The OTLP exporter and the Prometheus pull handler use the same lookup, so both expose the same bounds. Bounds are in the unit you record in; histograms created through the OTel API use the same lookup by metric name.
- In Prometheus these arrive as `<name>_bucket{le="..."}`, `<name>_count` and `<name>_sum`. `test/integration/lgtm/buckets_test.go` checks every `le` series and its count against the LGTM stack for the legacy API, a custom bucket override, and the OTel API.

### Exponential histograms

`stats.WithExponentialHistogram(maxSize, maxScale int32)` exports histograms as base-2 exponential histograms, which need no bucket configuration and keep relative error bounded.

- Each series starts at scale `maxScale` (range -10 to 20) and is downscaled when its values need more than `maxSize` buckets (at least 2) in the positive or the negative range.
- **A zero argument selects the default**: 160 buckets and scale 20, matching the OTel SDK. Scale 0 therefore cannot be chosen. Other out-of-range values make `NewClient` return `stats.ErrInvalidConfig`, even when OTLP is not enabled.
- **A metric with its own bounds from `WithHistogramBucketsFor` keeps explicit buckets.** `WithHistogramBuckets` then applies to no metric, since every other histogram is exponential.
- With cumulative temporality (the default), each export is merged into the series' earlier state: both are downscaled to the coarser of their scales, and further until the merged ranges fit `maxSize` buckets again.
- Only OTLP is affected. The receiving backend must support exponential histograms.

### Environment configuration

The OTLP exporter can be configured from the standard `OTEL_*` variables. This is opt-in: `OTEL_*` variables only fill in configuration, and the environment alone never enables OTLP. Enable it with `stats.WithOTLP(...)`, `stats.WithOTLPFromEnv()` or, in OTel mode, `otel.NewMeterProviderFromEnv()`.

**Precedence: explicit options win over environment variables, which win over defaults.** The metrics-specific `OTEL_EXPORTER_OTLP_METRICS_*` variable wins over the generic `OTEL_EXPORTER_OTLP_*` one. `stats.WithOTLP(&stats.OTLPConfig{...})` states every transport field of its struct, so zero values in it also beat the environment; use `WithOTLPFromEnv()` plus single-setting options (`WithOTLPExportTimeout`, `WithTemporality`, ...) to mix the two.

Supported variables:

- `OTEL_SDK_DISABLED`, `OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES`, `DEPLOYMENT_ENVIRONMENT`, `SERVICE_VERSION`
- `OTEL_EXPORTER_OTLP_PROTOCOL` (`grpc` or `http/protobuf`; `http/json` is rejected), `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_INSECURE`, `OTEL_EXPORTER_OTLP_HEADERS`, `OTEL_EXPORTER_OTLP_TIMEOUT`, `OTEL_EXPORTER_OTLP_COMPRESSION`, and each of these with the `OTEL_EXPORTER_OTLP_METRICS_` prefix
- `OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE` (`cumulative`, `delta` or `lowmemory`, which is exported as delta)
- `OTEL_METRIC_EXPORT_INTERVAL` (milliseconds; `WithFlushInterval` and `WithOTLPExportInterval` win)
- `STATS_DISABLE_GO_VERSION_REPORTING` (this library's own, for the version gauges)

Also read: `OTEL_EXPORTER_OTLP_[METRICS_]CERTIFICATE`, `CLIENT_CERTIFICATE` and `CLIENT_KEY` (files read by this library, which passes the resulting `tls.Config` to the SDK), `OTEL_EXPORTER_OTLP_METRICS_DEFAULT_HISTOGRAM_AGGREGATION` (`explicit_bucket_histogram` or `base2_exponential_bucket_histogram`) and `OTEL_METRIC_EXPORT_TIMEOUT`. Not supported, with no effect: any other `OTEL_*` variable. The endpoint, TLS mode, headers, timeout and compression handed to the SDK exporters are always the ones this library resolved, so SDK-side environment reading cannot change them. A malformed supported value makes `NewClient` return an error wrapping `stats.ErrInvalidConfig` that names the variable. Values and defaults are listed in [usage.md](usage.md#environment-variables).

### OTEL_SDK_DISABLED

With `OTEL_SDK_DISABLED=true` (case-insensitive), `stats.NewClient`, `otel.NewMeterProvider` and `otel.NewMeterProviderFromEnv` start no pipeline, exporter or runtime collector and dial nothing. Every instrument becomes a no-op, `ForceFlush` and `Shutdown` return nil, observable callbacks are never registered, and `Client.Disabled()` reports the state.

### Version metrics

On the first successful record of a root client, the library also records the gauges `stats_version` (the module version, or `(devel)`) and `go_version` (`runtime.Version()`, skipped for `devel` toolchains), each with value 1, the version in an attribute of the same name, and tagged with service and environment only. This applies in both modes and adds two series per process. `stats.WithVersionReporting(false)` or `STATS_DISABLE_GO_VERSION_REPORTING=true|TRUE|yes|1` turns it off; the option wins over the variable.

### Resource

`service.name`, `deployment.environment` and `service.version` resolve in this order (later wins): `OTEL_RESOURCE_ATTRIBUTES` < `OTEL_SERVICE_NAME` / `DEPLOYMENT_ENVIRONMENT` / `SERVICE_VERSION` < explicit options (`WithServiceName`, `WithEnvironment`, `WithOTLPResourceAttributes`, `otel.WithResource`). Missing identity falls back to `unknown_service` / `unknown`. `OTEL_RESOURCE_ATTRIBUTES` values are percent-decoded as in the OTel SDK; a malformed escape is kept unchanged.

`service.instance.id` follows the same order and defaults to the process hostname (a random hex ID if there is none). Without it, two replicas of a service export identical series, so their cumulative counters interleave as false resets and gauges flap between replicas. The hostname is used instead of the per-process UUID the semantic conventions suggest: it is unique per container or ECS task and stays the same across in-place restarts, so it does not mint new series on every restart.

The resource schema URL is exported as `ResourceMetrics.schema_url`. It comes from the `otel.WithResource` resource (for example `resource.NewWithAttributes(semconv.SchemaURL, ...)`) or from `stats.WithOTLPResourceSchemaURL`; it is empty by default.

### Metric Metadata

`Description` and `Unit` are exported on the OTLP metric for observable instruments (`metric.WithDescription` / `metric.WithUnit`) and for legacy observations recorded with `stats.WithDescription` / `stats.WithUnit`. Synchronous OTel instruments do not export them yet (see [Limitations](#limitations)).

### Attributes and Cardinality (all exporters)

Enforced in the shared pipeline, so Datadog, Prometheus (StatsD) and CloudWatch EMF receive the same sanitized attributes:

- Attribute keys must be one or more identifier segments joined by single dots: `^[a-zA-Z_][a-zA-Z0-9_]*(\.[a-zA-Z_][a-zA-Z0-9_]*)*$`. OTel semantic-convention keys such as `http.method`, `http.route` and `http.response.status_code` are accepted and exported unchanged; keys are never rewritten. An observation with any other key (`bad..key`, `.key`, `key.`, `http.1x`, `http-method`, non-ASCII) is rejected with `stats.ErrInvalidTagKey`: nothing is recorded, no series slot is used and no drop is counted. This is stricter than the OTel specification, which allows any non-empty key, so semantic-convention templates with free-form segments (for example `http.request.header.content-type`) are rejected. Metric names are not checked by this rule.
- Prometheus' OTLP translation maps both `http.method` and `http_method` to the label `http_method`; do not send both spellings on one metric. The Prometheus StatsD exporter writes each attribute into the dotted metric path as `key_value`, so a dotted key adds path segments, as in v1.0.1.
- String values are capped at 256 runes. Attributes reach every backend sorted by key with duplicate keys collapsed (last value wins), and only the first 10 keys in lexical order are kept.
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

`otel.NewMeterProviderFromEnv(opts...)` is `NewMeterProvider` with `stats.WithOTLPFromEnv()` prepended, so OTLP is configured from the environment and `OTEL_SDK_DISABLED` is honored; later options win over the environment.

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

### SemVer exception (v1.1.0)

v1.1.0 is released as a minor version although it changes behavior observable by v1.0.x consumers. The Go API only gains symbols (`gorelease` reports it valid), but: OTLP sums and histograms are cumulative by default (were delta); malformed attribute keys are rejected (`ErrInvalidTagKey`); the D10 limits apply (10 attributes, 256-rune values, 2000 series per metric); and `Shutdown`/`Close` drain the buffer before returning. A v2 release would need the module path `/v2`, which the project does not adopt. Pin v1.0.1 to keep the old behavior, or use `WithTemporality(stats.Delta)` for delta export. See the CHANGELOG.

### Current Limitations

1. **UpDownCounter is a gauge**: synchronous `UpDownCounter.Add(n)` records a gauge whose value is `n`, the latest increment, not a running total. Observable UpDownCounters export the observed value as a gauge. Neither is exported as a non-monotonic OTLP Sum.
2. **No Views**: Metric views are not implemented; cardinality is bounded by the fixed limits above.
3. **No Readers**: Custom metric readers are not supported; export is push-only through the pipeline.
4. **Units are not converted**: `Client.Timing` records milliseconds into a histogram, while the default buckets are in seconds. Use `Client.Observe` (a `time.Duration`, recorded in seconds) or `Histogram` with seconds, or set `WithHistogramBuckets`. `Timing` is unchanged; `Observe` is available through the optional `stats.DurationObserver` interface.
5. **Counters accept negative values** in the legacy API (`Client.Counter`); they are not rejected.
6. **OTLP environment configuration is partial**: only the variables listed under [Environment configuration](#environment-configuration) are read, and only when OTLP is enabled. There is no custom temporality or aggregation selector.
7. **Prometheus OTLP ingestion requires cumulative temporality** (the default); `WithTemporality(stats.Delta)` series are dropped by Prometheus' OTLP receiver.
8. **Synchronous instruments drop description and unit**: `metric.WithDescription` / `metric.WithUnit` on synchronous OTel instruments are accepted but not exported; observable instruments export them.
9. **Values are float64**: the pipeline carries every value as `float64`, so `Int64*` instruments (synchronous and observable) are exported as OTLP double points, and integers with magnitude above 2^53 (9007199254740992) are rounded to the nearest representable double.
10. **Cumulative timestamps can drift into the future**: each cumulative point of a series is stamped at least 1 ms after the previous one (see [Temporality](#temporality)). A series exported more than 1000 times per second therefore runs ahead of wall time, for example about 480 s after 2 minutes at 5000 exports/s, and Prometheus can reject it once the drift passes its future-sample tolerance. The library does not bound the drift; keep the export rate per series below 1000/s (a longer `WithFlushInterval`, fewer explicit `Flush` calls).
11. **`Flush` racing `Shutdown` (reported, not fixed)**: Copilot reported that a `Flush` that passes the shutdown check just before `Shutdown` starts can wait on a worker that `Shutdown` has already stopped. Our reading of the code is that the waiting `Flush` also selects on the pipeline context, which `Shutdown` cancels after its own broadcast, so it should be released with `ErrClientClosed` once `Shutdown`'s drain ends, possibly after a partial flush. That reading is not proven by a committed test (only a one-off manual simulation of a single interleaving), so treat an unbounded wait as possible. Mitigation: do not call `Flush` concurrently with `Shutdown`, and always pass `Flush` a context with a deadline. Tracked in https://github.com/convoy-road-trips-app/stats/issues/4.
12. **Observable instrument creation caches a failed first attempt**: if the first creation of an observable instrument has only a nil callback, `ErrNilCallback` is returned but the instrument stays cached. Creating the same instrument again with a valid callback returns the cached one with a nil error and registers no callback, so it never records. Create each observable instrument once, with valid callbacks. Tracked in https://github.com/convoy-road-trips-app/stats/issues/4.
13. **Accumulator comments describe the wrong failure semantics**: a failed OTLP export still advances the retained series state, on purpose, so the next cumulative point includes the interval the collector did not receive (see [Temporality](#temporality)). Two comments in `exporters/otlp/accumulator.go` say the state is committed only on success. The behavior is correct; the comments are not. Tracked in https://github.com/convoy-road-trips-app/stats/issues/4.

### Planned Features

- [x] Async/Observable instruments (v1.1.0)
- [x] Cumulative temporality support (v1.1.0, default)
- [ ] Non-monotonic Sum export for UpDownCounters
- [ ] Metric views for cardinality control
- [ ] Custom metric readers

## Timing steps with a Clock

`otel.NewClock(histogram, opts...)` times the sequential steps of one operation into any `metric.Float64Histogram`, in seconds, with a `stamp` attribute naming the step (this SDK's histogram or any other). `Stamp(ctx, name)` records the time since the previous step, and `Stop(ctx)` records the total under `stamp="total"`. `NewClockAt`, `StampAt` and `StopAt` take an explicit time. A clock is not safe for concurrent use; use constant step names, because each distinct name is a new series. Without the OTel API, use `client.Clock(name)` (see [usage.md](usage.md#clock)).

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
