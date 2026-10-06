# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.4.0] - Unreleased

segmentio/stats parity release. It ports the features of segmentio/stats v5.11.0 that are listed in the [migration tables](README.md#migrating-from-segmentiostats) onto the existing pipeline, with OpenTelemetry semantics. It is not a drop-in replacement: the "Not ported" rows in those tables list what is left out, including `influxdb`, `veneur`, the deprecated custom `otlp.Handler`, `grafana`, `util/objconv`, `cmd/dogstatsd` and parts of `httpstats`, `netstats`, `procstats` and the OTLP configuration. The Go API only gains symbols: `Recorder` and `Timing` are unchanged.

### Behavior changes

- The Datadog exporter strips the tag `http_req_path` from every metric by default (`DatadogConfig.Filters`, as in segmentio). Set `Filters` to an empty, non-nil slice to keep every tag.
- Two extra gauges, `stats_version` and `go_version`, are recorded once per process unless version reporting is turned off (see below).

### Added

- **Custom exporters**: `stats.WithExporter(Exporter)` registers any `stats.Exporter` (an alias of `models.Exporter`) next to the built-in backends, with its own entry in `ExporterErrors` and a unique-name check (`ErrInvalidConfig`). `exporters.Multi(name, defaultTimeout, children...)` fans out to several exporters (concurrent, each bounded on its own, panics recovered) and `exporters.Filtered(e, filter)` passes a subset of each batch. Batches are shared between exporters, so exporters must not modify them.
- **Context tags**: `stats.ContextWithTags`, `stats.ContextAddTags` and `stats.ContextTags` attach `attribute.KeyValue` tags to a context; every metric recorded with it carries them. Order of application: view tags, context tags, the metric's own attributes, explicit options. Tags pass the same key validation and cardinality limits as option tags, so use low-cardinality values and never request IDs.
- **Sub-clients**: `(*Client).WithPrefix(prefix, opts...)` and `(*Client).WithTags(opts...)` return views that share the pipeline. `Close` and `Shutdown` on a view do nothing; `Flush` and `Stats` act on the root. `*NoOpClient` has the same methods.
- **`Observe`**: `(*Client).Observe(ctx, name, time.Duration, ...MetricOption)` records a duration in seconds, with the optional `stats.DurationObserver` interface for code that holds a `Recorder`. `Timing` is unchanged and still records milliseconds.
- **`Clock`**: `(*Client).Clock(name, opts...)`, `stats.NewClock`, `NewClockAt` and `Clock.Stamp`/`StampAt`/`Stop`/`StopAt` time sequential steps as one histogram in seconds with a `stamp` attribute (`stats.StampTag`, `stats.StampTotal`). `otel.NewClock` does the same for any `metric.Float64Histogram`.
- **`Report`**: `stats.Report` and `stats.ReportAt` record structs described by `metric`, `type` and `tag` struct tags (bool, int, uint, float and `time.Duration` values, converted to `float64`, exact up to 2^53). An unsupported field returns `ErrUnsupportedReportField` before anything is recorded.
- **Per-metric buckets**: `stats.WithHistogramBucketsFor(name, bounds...)` overrides the histogram bounds of one metric. The OTLP exporter and the Prometheus pull handler share the lookup.
- **`OTEL_*` environment configuration**: `stats.WithOTLPFromEnv()` (and `otel.NewMeterProviderFromEnv`) read `OTEL_EXPORTER_OTLP_[METRICS_]{PROTOCOL,ENDPOINT,INSECURE,HEADERS,TIMEOUT,COMPRESSION}`, `OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE` and `OTEL_METRIC_EXPORT_INTERVAL`; `OTEL_SERVICE_NAME` and `OTEL_SDK_DISABLED` are read by every client. Explicit options win over the environment, which wins over defaults, and the environment alone never enables OTLP. `stats.WithOTLPExportInterval` and `stats.WithOTLPExportTimeout` set the interval and the timeout explicitly. Unsupported variables (`OTEL_EXPORTER_OTLP_CERTIFICATE`, the `CLIENT_*` variables, the histogram aggregation variable) have no effect. See `docs/usage.md`.
- **Exponential histograms**: `stats.WithExponentialHistogram(maxSize, maxScale)` exports OTLP histograms as base-2 exponential histograms. Zero selects the default of either argument (160 buckets, scale 20), so scale 0 cannot be chosen, and a metric with its own bounds from `WithHistogramBucketsFor` keeps explicit buckets.
- **Datadog**: `DatadogConfig.Endpoint` (`host:port`, `udp://` and `unixgram://` addresses), `BufferSize` (default 1432 for UDP and 8192 for unixgram, at most 65507; lines are batched into datagrams and never split, an oversized line is dropped and reported), `Filters` (default `http_req_path`), `UseDistributions` and `DistributionPrefixes` (matched against the whole metric name).
- **Prometheus pull**: `exporters/prometheus.Handler` is an `http.Handler` with cumulative in-memory state (counters exposed as `_total`, histograms as `_bucket`/`_sum`/`_count`, series expiring after `MetricTimeout`, default 2 minutes), registered with `stats.WithPrometheusHandler`. It is independent of the StatsD push exporter, and both can run in one client.
- **`httpstats`**: `NewHandler`, `NewHandlerWith`, `NewTransport`, `NewTransportWith`, `RequestWithTags`, `RequestTags` and `SetDefaultRecorder` record `http.server.*` and `http.client.*` metrics with the standard OTel names. `http.route` holds only the route template and is omitted when unset; paths and URLs are never recorded.
- **`netstats`**: `NewConn`, `NewListener`, `NewHandler` (and the `...With` variants), the `Handler` interface, `WithZones`, `WithFlushInterval` and `SetDefaultRecorder` record `conn.*` metrics with the segmentio names. `netstats.BaseConn`, `WithZoneDiscovery` (address-based zone discovery) and error counting for the deadline setters were added. Unlike segmentio, read and write totals are batched per connection and flushed on close and every 10 seconds instead of being recorded on every call.
- **`iostats`**: `CountReader`, `CountWriter`, `ReaderFunc`, `WriterFunc` and `CloserFunc`.
- **`statstest`**: `NewClient(t, opts...)`, `Exporter` (`Metrics`, `Clear`, `FlushCalls`), `Flush(t, client)` and the DogStatsD test server (`DogStatsDServer`, `NewDogStatsDServer`, `ListenAndServeDogStatsD`, `ServeDogStatsD` and the handler and message types).
- **`debugstats`**: `Exporter{Dst, Grep}` prints every metric as one StatsD-format line.
- **Runtime metrics**: memstats-style `memory.*` and `gc.*` metrics derived from `runtime/metrics` (no stop-the-world), process metrics, and Linux taskstats delay metrics (`runtimemetrics.Get`, `DelayInfo`, `ParseTaskstatsReply`, `IsUnsupported`, and `WithRuntimeDelayMetrics()`). See `docs/runtime_metrics.md`.
- **Documentation and examples**: README, `docs/usage.md`, `docs/otel_compliance.md` and `docs/runtime_metrics.md` cover the features above, and `examples/` gains `report`, `clock`, `httpstats`, `netstats`, `prometheus-pull` and `debugstats`.
- **Datadog events**: `Client.Event(ctx, DatadogEvent)` sends a DogStatsD event straight over the Datadog connection (not through the metric buffer) within the UDP timeout. Tags are view tags, then context tags, then `ev.Tags` (the later wins), validated like metric tags (`ErrInvalidTagKey`), then filtered by the Datadog `Filters`. It returns `ErrDatadogNotConfigured` without a Datadog backend, `ErrEventTooLarge` above the Datadog `BufferSize`, `ErrClientClosed` after the root closes, and nil on a disabled client. Failures are returned and counted in the new `ClientStats.EventsDropped` and `Pipeline.ExporterErrors["datadog"]`. Views send through the root; the optional `EventSender` interface (implemented by `*Client` and `*NoOpClient`) lets code check for support, and the Datadog exporter gains `SendEvent`.
- **Darwin process metrics**: `WithRuntimeProcessMetrics()` now also works on macOS, reading `getrusage(RUSAGE_SELF)` with the standard library only. It emits `cpu.usage.seconds`, `cpu.usage.percent`, `memory.usage.bytes{type=resident}` (peak RSS, in bytes), `memory.pagefault.count` and `threads.switch.count` with the Linux names and attributes; fields rusage does not provide are not emitted. A failing call is counted once in `ExporterErrors["runtimemetrics.process"]`. Windows and other platforms still emit nothing. See `docs/runtime_metrics.md`.
- **Runtime delay metrics**: `WithRuntimeDelayMetrics()` (implies `WithRuntimeMetrics()`) adds the counters `cpu.delay.seconds`, `blockio.delay.seconds`, `swapin.delay.seconds` and `freepages.delay.seconds` under the runtime prefix, read from Linux taskstats (needs `CAP_NET_ADMIN` or root and kernel delay accounting). Totals are emitted as increments. The first read failure, including on every non-Linux platform, is counted once in `ExporterErrors["runtimemetrics.delay"]` and disables delay collection. See `docs/runtime_metrics.md`.
- **Linux process metrics**: `WithRuntimeProcessMetrics()` (implies `WithRuntimeMetrics()`) adds `cpu.usage.seconds`, `cpu.usage.percent`, `memory.usage.bytes`, `memory.available.bytes`, `memory.total.bytes`, `memory.pagefault.count`, `files.open.count`, `files.open.max`, `threads.count` and `threads.switch.count` under the runtime prefix, read from `/proc` (and cgroup v2 `memory.max`). Other platforms emit nothing. An unreadable source is skipped and counted once in `ExporterErrors["runtimemetrics.process"]`. See `docs/runtime_metrics.md`.
- **`OTEL_SDK_DISABLED`**: when it is `true` (case-insensitive), `NewClient` and `otel.NewMeterProvider` start no pipeline, exporter or runtime collector and dial nothing. Every recording method (and views from `WithPrefix`/`WithTags`) does nothing and returns nil without validating input; `Flush`, `Shutdown` and `Close` return nil; `Stats()` returns a zero `ClientStats` with an empty `ExporterErrors` map; observable callbacks are never registered. `Client.Disabled()` reports the state.
- **Version reporting**: the first successful record on a root client also records the gauges `stats_version` (the module version of this library, or `(devel)`) and `go_version` (`runtime.Version()`, skipped for `devel` toolchains), each with value 1 and tagged with `service` and `environment` only. This adds two series per process, and tests that count exported metrics see two extra observations. Disable both with `WithVersionReporting(false)` or `STATS_DISABLE_GO_VERSION_REPORTING=true|TRUE|yes|1`; the option wins over the environment. `statstest.NewClient` disables reporting by default.

### Fixed

- **OTLP transport settings are always the ones this library resolved**: the SDK exporters read `OTEL_EXPORTER_OTLP_*` themselves before our options ran, so a stray `OTEL_EXPORTER_OTLP_CERTIFICATE` could break an insecure endpoint and environment headers or compression could leak into an explicitly configured exporter. The endpoint (including its path), TLS mode, headers, timeout and compression are now passed explicitly. `OTEL_EXPORTER_OTLP_CERTIFICATE`, the `*_CLIENT_*` variables and the histogram aggregation variable are unsupported and have no effect. An `http://` endpoint URL implies an insecure connection.
- **Shared metrics are no longer mutated by the OTLP and Prometheus exporters**: attribute sets are built from a clone of the metric's attributes, because `attribute.NewSet` sorts its input in place and exporters run in parallel on shared batches.

## [1.3.0] - 2026-10-06

### Added

- **Default `service.instance.id` resource attribute**: OTLP exports now set `service.instance.id` to the hostname when neither `OTEL_RESOURCE_ATTRIBUTES` nor `WithOTLPResourceAttributes` provides one (a random hex ID if there is no hostname). Previously replicas of one service exported identical series, so cumulative counters from different processes interleaved as false resets and gauges flapped between replicas. Prometheus-compatible backends receive it as the `instance` label, so expect one series per replica; aggregate with `sum`/`max` across `instance`. To opt out, set `service.instance.id` explicitly (for example `OTEL_RESOURCE_ATTRIBUTES=service.instance.id=<name>`).

## [1.2.2] - 2026-10-01

### Fixed

- **Cumulative OTLP exports omitted series not observed in the interval**: with cumulative temporality (the default) only series observed since the previous export were sent, so sparse series went stale in Prometheus-compatible backends. Every export now carries all known counters, histograms and gauges (gauges at their last value, unchanged `StartTimeUnixNano`), and an interval without observations re-exports the full state, as the OTel SDK does on each collection. Delta temporality still exports only observed series.

## [1.2.1] - 2026-10-01

### Fixed

- **OTLP exports were cancelled by the 100ms UDP deadline**: background flushes bounded every exporter by `UDPTimeout`, so any real OTLP round trip (gRPC dial plus export) failed with `context deadline exceeded`, the batch was dropped, and a Shutdown during the export reported that error. OTLP exports are now bounded by `OTLPConfig.ExportTimeout` (default 10s).

## [1.2.0] - 2026-10-01

Documentation-only release. There are **no code or behavior changes**: the Go API, exports and defaults are identical to v1.1.0, and the module is unchanged apart from its version. Upgrading from v1.1.0 needs no action.

### Changed

- **Architecture diagrams**: the README's ASCII architecture drawing is replaced by a compact Mermaid diagram, and `docs/architecture.md` gains source-checked Mermaid diagrams for the component overview, the recording flow (validation, cardinality, pressure drops, enqueue), the export and exporter fan-out flow, and the Flush/Shutdown lifecycle, including the documented Flush/Shutdown race limitation. The original design notes in that file are kept below them, marked as design notes.
- **Docker example**: `examples/docker/README.md` shows the demo, collector and Prometheus topology as a Mermaid diagram.
- **README claims aligned with the code**: the feature list and design principles no longer promise zero allocation or that recording never blocks under any load, and now say that exporters are isolated from each other's errors and panics but a batch completes when its slowest exporter returns.
- Broken links to a root `ARCHITECTURE.md` now point to `docs/architecture.md`.

## [1.1.0] - 2026-10-01

OpenTelemetry conformance release. See [docs/otel_compliance.md](docs/otel_compliance.md) for the full semantics and limitations.

### SemVer exception

This release is v1.1.0, not v2.0.0, although it changes behavior that v1.0.x consumers can observe. The public Go API only gains symbols (`gorelease -base=v1.0.1 -version=v1.1.0` reports v1.1.0 as valid), but these behaviors differ from v1.0.1:

- OTLP sums and histograms are **cumulative by default** (they were delta).
- **Malformed attribute keys are rejected** with `ErrInvalidTagKey` (they were accepted).
- **D10 cardinality limits** apply: 10 attributes per observation, 256-rune values, 2000 series per metric (`ErrCardinalityLimit`).
- `Shutdown`/`Close` **drain** buffered metrics before returning, so they can take up to the context deadline.

A v2 release would need the module path `github.com/convoy-road-trips-app/stats/v2`, which the project does not adopt. Pin `v1.0.1` if you depend on the old behavior; `WithTemporality(stats.Delta)` restores delta export. Treat these four changes as a documented exception to the "adheres to Semantic Versioning" statement above.

### Added

- **OTLP explicit-bucket histograms**: observations are aggregated per attribute set with count, sum, min, max and bucket counts. Default bounds are the D9 seconds buckets (`0.005 … 10`); `WithHistogramBuckets` overrides them (finite, strictly increasing).
- **Temporality control**: `WithTemporality(stats.Cumulative | stats.Delta)`. A delta point starts where the previous point of its series ended and never ends before it starts, even when a worker exports older observations late.
- **Full OTLP resource**: `service.name`, `deployment.environment` and `service.version` from options, `OTEL_SERVICE_NAME` / `DEPLOYMENT_ENVIRONMENT` / `SERVICE_VERSION`, `OTEL_RESOURCE_ATTRIBUTES` (percent-decoded values), `WithOTLPResourceAttributes` and `otel.WithResource`. The resource schema URL is exported as `ResourceMetrics.schema_url`, from `otel.WithResource` or `WithOTLPResourceSchemaURL`.
- **Cardinality limits**: at most 10 attributes per observation, 256-rune values and 2000 series per metric (`WithMaxCardinality`), with `ErrCardinalityLimit` and the `telemetry_dropped_labels_total{reason}` counter.
- **Reliable flush**: `Client.Flush(ctx)` (also `NoOpClient.Flush`, both through the new `Flusher` interface; `Recorder` is unchanged), `MeterProvider.ForceFlush(ctx)`, a draining `Shutdown(ctx)` and `WithOTLPRetry(initial, maxInterval, maxElapsed)`.
- **Exemplars**: counter and histogram observations under a sampled span carry `trace_id`/`span_id` exemplars in OTLP.
- **Observable instruments**: `Int64/Float64 ObservableCounter`, `ObservableUpDownCounter` and `ObservableGauge`, `Meter.RegisterCallback`, and `otel.WithCollectionInterval` (default 10s).
- **Metric metadata**: `WithDescription` and `WithUnit` metric options; OTLP exports the description and unit of observable instruments and of observations recorded with these options.
- **Usage guide and examples**: `docs/usage.md` (install, minimal usage, options reference), `examples/quickstart` (the guide's snippet, kept identical by a test) and `examples/docker` (demo service, OpenTelemetry Collector and Prometheus with `_bucket` series, tested in CI behind the `docker` build tag).
- **LGTM CI job**: the `grafana/otel-lgtm` integration tests run in CI again and check histogram `_bucket{le}` series and counts at the Prometheus query API.

### Changed

- **OTLP sums and histograms are cumulative by default** (previously delta). Use `WithTemporality(stats.Delta)` for the old behavior. Prometheus' OTLP receiver ingests only cumulative series.
- **Malformed attribute keys are rejected**: a key must be one or more identifier segments joined by single dots, `^[a-zA-Z_][a-zA-Z0-9_]*(\.[a-zA-Z_][a-zA-Z0-9_]*)*$`. OTel semantic-convention keys such as `http.method` and `http.route` are accepted and exported unchanged. Keys with an empty segment (`bad..key`, `.key`, `key.`), a segment starting with a digit, or any other character (`-`, space, `/`, non-ASCII) return `ErrInvalidTagKey` and nothing is recorded, for every backend; v1.0.1 accepted them. OTel instruments drop such observations silently.
- Datadog, Prometheus (StatsD) and CloudWatch EMF receive the same sanitized and trimmed attributes as OTLP.
- `Client.Shutdown`/`Close` now drain buffered metrics, and Flush/Shutdown report failures of background exports already in flight. When the runtime collector and the pipeline both fail, `Shutdown` returns both errors joined.
- `DropOldest` evicts the oldest metric only when the incoming one takes its place, and `WithBufferSize(1)` holds two metrics; `WithOTLP` copies `HistogramBuckets`.

### Fixed

- Cumulative OTLP points of a series are stamped at least 1 ms after the previous point. Concurrent workers could export a larger total with an older or same-millisecond timestamp, which Prometheus dropped as a duplicate sample. A sum point merged from a batch is stamped no earlier than the newest observation it includes.
- With cumulative temporality, a failed OTLP export no longer loses its interval: the next point includes it.
- `RingBuffer.Push` no longer reports a spurious `ErrBufferFull` under concurrent pops.
- `WithOTLP` copies its config, so one option can configure several clients.
- Attributes reach every backend sorted and deduplicated, so permutations of one attribute set are one StatsD series within the cardinality limit.
- A sum point keeps the sampled exemplar with the latest observation time, whatever the batch order.
- `RingBuffer` no longer loses or duplicates an item while full. `Push` never waits for a consumer to release a slot (it reports the buffer full), and `DropOldest` removes the oldest item with the new non-waiting `RingBuffer.TryPop`.

### Known Limitations

- `UpDownCounter` (sync and observable) is exported as a gauge; the sync `Add(n)` gauge value is the latest increment, not a running total.
- Synchronous OTel instruments do not export `WithDescription` / `WithUnit`.
- Values are carried as `float64`: `Int64*` instruments are exported as OTLP double points, exact only up to 2^53.
- `Client.Timing` records milliseconds while the default buckets are in seconds.
- `Client.Counter` accepts negative values.
- `OTEL_EXPORTER_OTLP_ENDPOINT` is not read; no views or custom readers.
- Background exports, OTLP included, are bounded by `WithUDPTimeout` (100 ms default).
- Reported race (Copilot, PR #3): a `Flush` that passes the shutdown check just before `Shutdown` starts may wait on a worker that `Shutdown` has already stopped. We expect it to stay blocked for about as long as `Shutdown`'s drain takes, because `Flush` also waits on the pipeline context, which `Shutdown` cancels after its own broadcast; it would then return `ErrClientClosed`, possibly after flushing only some workers. This reasoning has not been proven by a committed test, and the review's claim that `Flush` can block forever has not been ruled out. Do not call `Flush` concurrently with `Shutdown`, and pass `Flush` a context with a deadline. Tracked in https://github.com/convoy-road-trips-app/stats/issues/4.
- Creating an observable instrument whose only callback is nil returns `ErrNilCallback` but keeps the instrument cached; creating it again with a valid callback returns the cached instrument without error and registers nothing, so it records nothing. Create each observable instrument once, with valid callbacks. Tracked in https://github.com/convoy-road-trips-app/stats/issues/4.
- OTLP exporter behavior and comments disagree: a failed cumulative export still advances the retained series state (so the next point includes the lost interval), while two code comments in `exporters/otlp/accumulator.go` describe the state as uncommitted until transport succeeds. The behavior is the intended one; only the comments are wrong. Tracked in https://github.com/convoy-road-trips-app/stats/issues/4.
- Cumulative OTLP timestamps can run ahead of wall time: each point of a series is stamped at least 1 ms after the previous one, so a series exported more than 1000 times per second drifts into the future (about 480 s after 2 minutes at 5000 exports/s), and Prometheus can reject it once the drift passes its future-sample tolerance. Keep the export rate per series below 1000/s (raise `WithFlushInterval`, or avoid frequent `Flush`); it is not bounded in the library.

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

[1.4.0]: https://github.com/convoy-road-trips-app/stats/compare/v1.3.0...HEAD
[1.3.0]: https://github.com/convoy-road-trips-app/stats/compare/v1.2.2...v1.3.0
[1.2.2]: https://github.com/convoy-road-trips-app/stats/compare/v1.2.1...v1.2.2
[1.2.1]: https://github.com/convoy-road-trips-app/stats/compare/v1.2.0...v1.2.1
[1.2.0]: https://github.com/convoy-road-trips-app/stats/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/convoy-road-trips-app/stats/compare/v1.0.1...v1.1.0
[1.0.0]: https://github.com/convoy-road-trips-app/stats/releases/tag/v1.0.0
