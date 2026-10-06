# Install and usage

## Install

```bash
go get github.com/convoy-road-trips-app/stats@v1.2.2
```

Requires Go 1.26 or newer. Upgrading from v1.0.x? v1.1.0 changes some behavior (cumulative OTLP by default, key rejection, cardinality limits, draining `Shutdown`); see [Upgrading from v1.0.x](../README.md#upgrading-from-v10x-semver-exception).

## Minimal usage

Create one client at startup, record from anywhere, flush when an invocation ends, and shut down on exit. This program is `examples/quickstart/main.go`; a test keeps this copy identical to it, and CI builds it.

```go
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/convoy-road-trips-app/stats"
)

func main() {
	// Build the client once at startup. Without a backend option it records
	// but exports nowhere; here it pushes OTLP/HTTP to a local collector.
	client, err := stats.NewClient(
		stats.WithServiceName("checkout"),
		stats.WithEnvironment("production"),
		stats.WithOTLP(&stats.OTLPConfig{
			Endpoint: "localhost:4318",
			Insecure: true,
			Protocol: stats.OTLPProtocolHTTP,
		}),
		// Background exports are bounded by this timeout (default 100 ms);
		// raise it for a remote collector.
		stats.WithUDPTimeout(5*time.Second),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "create client:", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// Recording never blocks. The error reports a dropped observation (full
	// buffer, cardinality limit, invalid attribute key).
	if err := client.Counter(ctx, "checkout_orders", 1,
		stats.WithAttribute("payment", "card"),
	); err != nil {
		fmt.Fprintln(os.Stderr, "counter:", err)
	}
	_ = client.Gauge(ctx, "checkout_queue_depth", 12)
	_ = client.Histogram(ctx, "checkout_latency_seconds", 0.145, // seconds
		stats.WithAttributes(map[string]string{"route": "/pay", "http.method": "POST"}),
	)

	// Export everything recorded so far, for example at the end of a Lambda
	// invocation. Flush is also available as the stats.Flusher interface.
	flushCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Flush(flushCtx); err != nil {
		fmt.Fprintln(os.Stderr, "flush:", err)
	}

	// Shutdown drains whatever is still buffered. Call it once, on exit.
	if err := client.Shutdown(flushCtx); err != nil {
		fmt.Fprintln(os.Stderr, "shutdown:", err)
	}
}
```

| Call | Records | Notes |
|---|---|---|
| `Counter(ctx, name, v, opts...)`, `Increment`, `IncrementBy` | a sum | cumulative in OTLP by default |
| `Gauge(ctx, name, v, opts...)` | the latest value | |
| `Histogram(ctx, name, v, opts...)` | one observation | explicit buckets in OTLP; the default bounds are in **seconds** (`0.005 ... 10`) |
| `Timing(ctx, name, d, opts...)` | a duration in **milliseconds** | unchanged; does not match the default seconds buckets |
| `Observe(ctx, name, d, opts...)` | a duration in **seconds** (`d.Seconds()`) | matches the default buckets; prefer it to `Timing` for new code |
| `stats.WithAttribute(k, v)`, `stats.WithAttributes(map)` | attributes (tags) | see the limits below |

### Attributes

- Keys are identifier segments joined by single dots (`route`, `http.method`); anything else returns `ErrInvalidTagKey`.
- At most 10 attributes per observation, values capped at 256 runes, and 2000 series per metric name (`WithMaxCardinality`); a new series beyond that returns `ErrCardinalityLimit` and is counted in `telemetry_dropped_labels_total{reason}`.
- Do not put unbounded values (user IDs, request IDs) in attributes.

### Observe versus Timing

Both record a `time.Duration` into a histogram, in different units:

| | Unit | Matches default buckets (`0.005 ... 10`) | On `stats.Recorder` |
|---|---|---|---|
| `Timing` | milliseconds | no | yes |
| `Observe` | seconds | yes | no, see below |

`Observe` takes the same path as `Histogram`, so it never blocks, applies options and context tags the same way, and returns `ErrClientClosed` after `Close`. `Timing` is unchanged, so dashboards built on its millisecond values keep working. The `stats.Recorder` interface is unchanged too; `*Client` and `*NoOpClient` implement the optional `stats.DurationObserver` interface:

```go
if o, ok := recorder.(stats.DurationObserver); ok {
    err = o.Observe(ctx, "op.duration", time.Since(start))
}
```

### Sub-clients: WithPrefix and WithTags

```go
api := client.WithPrefix("api").WithTags(stats.WithAttribute("region", "eu"))
_ = api.Counter(ctx, "requests", 1)       // "api.requests", region=eu
_ = api.WithPrefix("v1").Counter(ctx, "requests", 1) // "api.v1.requests"
```

`WithPrefix(prefix, opts...)` and `WithTags(opts...)` return a `*Client` view that shares the parent's pipeline and lifecycle. Only the attributes of the options are used; other option effects are ignored.

- Prefix parts are joined with `.`, and empty parts are skipped.
- A later tag wins over an earlier one with the same key, so a child's tag overrides its parent's. View tags apply before context tags and the metric's own attributes.
- `Close` and `Shutdown` on a view do nothing and return nil. `Flush` and `Stats` act on the root client. Recording through a view fails with `ErrClientClosed` once the root is closed.
- `*NoOpClient` has the same two methods, so a `NoOpClient` can stand in for a view in tests.

### Context tags

```go
ctx = stats.ContextWithTags(ctx, attribute.String("tenant.tier", "gold"))
_ = client.Counter(ctx, "orders", 1) // carries tenant.tier=gold

stats.ContextAddTags(ctx, attribute.String("region", "eu")) // bool: false if ctx has no tag set
current := stats.ContextTags(ctx)                             // copy of the tags, or nil
```

- `ContextWithTags` returns a new context. It **replaces** tags that the parent context carried; pass `ContextTags(ctx)` along with the new tags to keep them.
- `ContextAddTags` appends in place, so the change is visible to every context that shares the tag set, and is safe for concurrent use. It returns false and does nothing when the context was not created by `ContextWithTags`.
- Order of application, later wins on a duplicate key: view tags, context tags, the metric's own attributes, explicit options.
- Context tags pass the same key validation and cardinality limits as option tags. An invalid key makes the recording fail with `ErrInvalidTagKey`.

> **Cardinality warning.** Tags become series dimensions. Use values with a small fixed set, such as a region, a tenant tier or a route template. **Never put request IDs, user IDs, session IDs or other unbounded values in context tags.** Each new value is a new series and counts against `WithMaxCardinality` (2000 per metric by default).

`httpstats.RequestWithTags` and `httpstats.RequestTags` are the same mechanism for an `*http.Request`.

### Report

`stats.Report(ctx, recorder, v, opts...)` records the metrics described by the struct tags of `v` (a struct, a pointer to one, or a slice or array of either). `stats.ReportAt(ctx, recorder, t, v, opts...)` stamps every metric with `t`, taking precedence over a `WithTimestamp` option.

| Struct tag | On | Meaning |
|---|---|---|
| `metric:"name"` | a value field | the metric name |
| `metric:"prefix"` | a struct field | name prefix, joined with `.` (`cache` + `hits` is `cache.hits`); a struct field without it is traversed without a prefix |
| `type:"counter"`, `"gauge"`, `"histogram"` | a value field | metric type; histogram by default |
| `tag:"key"` | a string field | attribute on the metrics of its struct and of nested structs; a tag on a nested struct overrides an inherited one, and an empty value is never attached |

Value fields may be `bool` (0 or 1), any int, uint or float width, `uintptr`, or `time.Duration` (reported in seconds). Everything is recorded as `float64`, so integers are exact up to 2^53 and larger magnitudes lose precision. Unexported fields are read like exported ones, and fields without a `metric` or `tag` struct tag are ignored.

A field that carries a `metric` or `tag` tag but has an unsupported kind (channel, map, pointer to a number, `time.Time`, a string metric), an unknown `type` tag, or a struct type that contains itself makes `Report` return an error wrapping `ErrUnsupportedReportField` before anything from that value is recorded. A failed recording does not stop the rest, and the returned error joins every failure. Metrics go through the recorder's `Counter`, `Gauge` and `Histogram`, so prefixes (from `WithPrefix`) and context tags apply. See `examples/report`.

### Clock

A `Clock` reports the durations of the sequential steps of one operation as one histogram, in seconds, with a `stamp` attribute:

```go
clock := client.Clock("job.duration", stats.WithAttribute("job", "nightly"))
// ... load ...
_ = clock.Stamp(ctx, "load")  // time since the clock started
// ... store ...
_ = clock.Stamp(ctx, "store") // time since the previous Stamp
_ = clock.Stop(ctx)           // time since the start, stamp="total"
```

`client.Clock(name, opts...)` and `stats.NewClock(recorder, name, opts...)` start a clock now; `stats.NewClockAt`, `StampAt` and `StopAt` take an explicit time. The attribute key and the final value are `stats.StampTag` (`stamp`) and `stats.StampTotal` (`total`). Observations use `Observe` when the recorder implements `DurationObserver`, otherwise `Histogram` with `d.Seconds()`. A clock is not safe for concurrent use, and every distinct step name is a new series, so use constant names. For OTel histograms there is `otel.NewClock(histogram, opts...)` with the same methods (without error returns). See `examples/clock`.

### Flush and shutdown

- `Flush(ctx)` exports everything recorded before the call and returns when it is exported or `ctx` is done. Call it at the end of a Lambda invocation.
- `Shutdown(ctx)` / `Close()` stop accepting observations and drain the buffer. Always call it before the process exits, or buffered metrics are lost.
- `Flush` is on `*Client` and `*NoOpClient` through the optional `stats.Flusher` interface; `stats.Recorder` has no `Flush`, so existing implementations keep compiling: `if f, ok := recorder.(stats.Flusher); ok { _ = f.Flush(ctx) }`.

### Testing code that records metrics

Accept a `stats.Recorder` and pass `stats.NewNoOpClient()` in tests (see `examples/testing`).

## Configuration reference

Options are passed to `stats.NewClient`. Defaults are those of `stats.DefaultConfig()`.

### Core

| Option | Default | Purpose |
|---|---|---|
| `WithServiceName(name)` | `unknown-service` | `service.name`; also read from `OTEL_SERVICE_NAME` for OTLP |
| `WithEnvironment(env)` | `development` | `deployment.environment` |
| `WithBufferSize(n)` | 16384 | ring buffer capacity (rounded up to a power of two, minimum 2) |
| `WithWorkers(n)` | 4 | goroutines that batch and export |
| `WithFlushInterval(d)` | 100 ms | how often a worker exports its batch |
| `WithUDPTimeout(d)` | 100 ms | bound for each **background** export, OTLP included; raise it for a remote collector |
| `WithMaxMemoryBytes(n)` | 10 MiB | memory cap for buffered metrics (`ErrMemoryLimit`) |
| `WithMaxCardinality(n)` | 2000 | series per metric name |
| `WithDropStrategy(s)` | `DropNewest` | `DropNewest` rejects the incoming metric on a full buffer; `DropOldest` evicts the oldest |
| `WithAdaptiveBatching(bool)` | off | larger batches under load |
| `WithRateLimit(rate, burst)` | off | cap on observations per second |
| `WithRuntimeMetrics()` | off | Go runtime metrics every 10 s, see [runtime_metrics.md](runtime_metrics.md) |
| `WithRuntimeProcessMetrics()` | off | implies `WithRuntimeMetrics()`; adds process metrics on Linux and Darwin |
| `WithExporter(e)` | none | registers a custom `stats.Exporter` next to the built-in backends; it needs a unique `Name()` (`ErrInvalidConfig` otherwise) and is shut down on `Close` |
| `WithVersionReporting(bool)` | on | the `stats_version` and `go_version` gauges (value 1, tagged with service and environment); `STATS_DISABLE_GO_VERSION_REPORTING=true\|TRUE\|yes\|1` turns them off, and the option wins over the variable |

Exporters that speak a UDP or Unix datagram protocol can embed `exporters.BaseExporter` (`exporters.NewBaseExporter(name, address, serializer)`, or `exporters.NewBaseExporterNetwork(name, network, address, serializer)` with `"udp"` or `"unixgram"`), which adds a connection pool and circuit breaker; `SendPackets(ctx, packets, count)` sends already serialized datagrams through them, and `Stats()` returns `exporters.ExporterStats`. `datadog.NewExporter(cfg)` builds the Datadog exporter directly.

`exporters.Multi(name, defaultTimeout, children...)` presents several exporters as one (children run concurrently, each bounded on its own, and a panic in one is reported as that child's error), and `exporters.Filtered(e, filter)` passes only the part of each batch that `filter` returns. A filter must return a subset of its input or copies, because batches are shared between exporters running in parallel.

### OTLP

| Option | Default | Purpose |
|---|---|---|
| `WithOTLP(&OTLPConfig{...})` | disabled | enables OTLP: `Endpoint` (`host:port`), `Insecure`, `Headers`, `Protocol` (`OTLPProtocolGRPC` default, port 4317; `OTLPProtocolHTTP`, port 4318), `ExportTimeout` (10 s) |
| `WithTemporality(stats.Cumulative \| stats.Delta)` | `Cumulative` | Prometheus' OTLP receiver only ingests cumulative |
| `WithOTLPFromEnv()` | off | enables OTLP and reads its settings from the `OTEL_EXPORTER_OTLP_*` variables; options after it win |
| `WithOTLPExportInterval(d)` | 100 ms | how often batches are handed to the exporters (same as `WithFlushInterval`); beats `OTEL_METRIC_EXPORT_INTERVAL` |
| `WithOTLPExportTimeout(d)` | 10 s | per-export deadline; beats `OTEL_EXPORTER_OTLP_TIMEOUT` |
| `WithHistogramBuckets([]float64)` | `0.005 ... 10` | explicit bounds in the metric's unit; finite and strictly increasing |
| `WithHistogramBucketsFor(name, bounds...)` | none | explicit bounds for one metric name, overriding the global bounds; shared by the OTLP exporter and the Prometheus pull handler |
| `WithExponentialHistogram(maxSize, maxScale)` | off | base-2 exponential histograms, see below |
| `WithOTLPRetry(initial, max, maxElapsed)` | SDK default | retry retryable failures |
| `WithOTLPResourceAttributes(attrs...)` | none | extra resource attributes; `OTEL_RESOURCE_ATTRIBUTES` is also read |

#### Exponential histograms

`WithExponentialHistogram(maxSize, maxScale int32)` makes the OTLP exporter send base-2 exponential histograms instead of explicit buckets.

- Every series starts at scale `maxScale`, in [-10, 20], and is downscaled when its values need more than `maxSize` buckets (at least 2) in its positive or its negative range.
- **Zero means the default** for either argument: 160 buckets and scale 20, as in the OTel SDK. Scale 0 itself therefore cannot be chosen. Values outside the ranges make `NewClient` return `ErrInvalidConfig`, even when OTLP is not enabled.
- **Explicit buckets stay explicit** for a metric that has its own bounds from `WithHistogramBucketsFor`. `WithHistogramBuckets` then applies to no metric, because every other histogram is exponential.
- Only the OTLP exporter is affected. The Prometheus pull handler and the StatsD backends keep their own histogram handling.

### Other backends

`WithDatadog(&DatadogConfig{...})`, `WithPrometheus(&PrometheusConfig{Host, Port, Prefix})` (StatsD push), `WithPrometheusHandler(h)` (pull) and `WithCloudWatch(&CloudWatchConfig{LogGroupName, Namespace, ...})` can be combined with OTLP; each receives every observation.

#### Datadog

| Field | Default | Purpose |
|---|---|---|
| `AgentHost`, `AgentPort` | none | DogStatsD agent address |
| `Endpoint` | empty | overrides host and port: `host:port`, `udp://host:port` or `unixgram:///abs/path` (Unix datagram socket, not on Windows) |
| `BufferSize` | 1432 (UDP), 8192 (unixgram) | largest datagram in bytes, at most 65507; whole lines are batched into datagrams and never split, and a single line larger than `BufferSize` is dropped and reported as an export error |
| `Tags` | none | global tags (`key:value`) added to every metric |
| `UseDistributions` | false | send every histogram as a distribution (`\|d`) instead of a histogram (`\|h`) |
| `DistributionPrefixes` | none | histograms whose full name starts with one of these prefixes are sent as distributions; counters and gauges are never affected, and `UseDistributions` takes precedence. The whole metric name is matched, including prefixes |
| `Filters` | `["http_req_path"]` | tag keys removed from every metric, from attributes and global tags. A nil slice selects the default; an empty non-nil slice keeps every tag |

`client.Event(ctx, stats.DatadogEvent{Title, Text, Timestamp, Host, Priority, AlertType, AggregationKey, SourceTypeName, Tags})` sends a DogStatsD event directly over the Datadog connection, not through the metric buffer, and blocks for at most the UDP timeout. `Title` and `Text` are required by the protocol; `stats.EventPriorityNormal`, `EventPriorityLow` and `EventAlertTypeError`, `EventAlertTypeWarning`, `EventAlertTypeInfo`, `EventAlertTypeSuccess` are the constants for `Priority` and `AlertType`. Tags are the view tags, then context tags, then `ev.Tags` (later wins), validated like metric tags, then filtered by `Filters`.

| Error | When |
|---|---|
| `ErrDatadogNotConfigured` | no Datadog backend |
| `ErrEventTooLarge` | the serialized event exceeds `BufferSize` |
| `ErrClientClosed` | the root client is closed |
| `ErrInvalidTagKey` | an event tag key is invalid |

A delivery failure is returned and counted in `ClientStats.EventsDropped` and `Pipeline.ExporterErrors["datadog"]`. On a disabled client `Event` returns nil. The optional `stats.EventSender` interface (implemented by `*Client` and `*NoOpClient`) lets code that holds a `stats.Recorder` check for support.

#### Prometheus: push versus pull

- **Push** (`WithPrometheus`) sends StatsD lines over UDP to a StatsD exporter, which Prometheus scrapes. The exporter holds the state.
- **Pull** (`WithPrometheusHandler(&prometheus.Handler{})`) keeps cumulative state in your process and renders it when the handler, an `http.Handler`, is scraped: counters accumulate and are exposed as `<name>_total`, gauges keep the newest value, and histograms expose `_bucket`, `_sum` and `_count`. Series that were not updated for `Handler.MetricTimeout` (default 2 minutes) expire.

`Handler` fields, all optional and set before first use: `TrimPrefix` (removed from metric names before normalization), `MetricTimeout`, and `Buckets` (histogram bounds per metric name; when nil, the client fills it from `WithHistogramBucketsFor` and `WithHistogramBuckets`, so a scrape and OTLP show the same bounds). Names and labels are normalized to the Prometheus character set; a label name collision or a metric family collision skips the offending metric and is reported as an export error under `prometheus-pull`. A scrape accepts `GET` and `HEAD`, answers other methods with 405, and gzips the response when asked. Metrics reach the handler asynchronously, so `Flush` before scraping in tests. A handler belongs to one client, and the client shuts it down on `Close` (the stored state stays readable). See `examples/prometheus-pull`.

The exposition code is reusable on its own: `prometheus.WriteFamilies(w, families)` renders `Family` values (each with `Series`, `Label` and, for histograms, `HistogramData`) in the text format (`prometheus.ContentType`), `NormalizeMetricName`, `NormalizeLabelName`, `CounterFamilyName` and `ExposedLabels` map names and attributes to exposed names, and a `FamilyRegistry` (`NewFamilyRegistry().Register(source, type)`) applies the first-registered-wins rule to family names. `Handler.ServeHTTP` serves a scrape and `Handler.WriteStats(w)` renders the same text to any writer. Failures wrap `ErrLabelCollision`, `ErrFamilyCollision` or `ErrInvalidFamily`. `prometheus.PullExporterName` (`prometheus-pull`) is the handler's exporter name, and `prometheus.DefaultMetricTimeout` is the 2-minute series expiry.

#### Types in `models`

The option and config types of this release are aliased in `stats` and defined in `models`: `stats.EventPriority` and `stats.EventAlertType` (event fields), `stats.OTLPExponentialHistogram` (`MaxSize`, `MaxScale`, with `Resolved()` and `Validate()`; defaults `models.DefaultExponentialHistogramMaxSize` 160 and `DefaultExponentialHistogramMaxScale` 20) and `stats.OTLPOverrides` (the OTLP settings stated through options, which beat the environment). `models` also has `DatadogConfig.ResolveEndpoint`, `Address` and `PacketSize`, `models.DefaultDatadogFilters()`, the buffer constants `DefaultDatadogUDPBufferSize` (1432), `DefaultDatadogUnixgramBufferSize` (8192) and `MaxDatadogBufferSize` (65507), the bucket helpers `BucketsFor` and `ValidateHistogramBuckets`, and the optional exporter interfaces `ExportTimeouter` (an exporter that bounds its own exports, which turns off the pipeline's default export deadline) and `IdleExporter` (an exporter with cumulative state that must run on every flush interval, even without observations).

## Environment variables

`OTEL_*` variables only fill in configuration. The environment never enables OTLP: use `WithOTLP`, or `WithOTLPFromEnv()` (`otel.NewMeterProviderFromEnv()` in OTel mode).

**Precedence: explicit options win over environment variables, which win over defaults.** A signal-specific `OTEL_EXPORTER_OTLP_METRICS_*` variable wins over the generic `OTEL_EXPORTER_OTLP_*` one, and an empty value counts as unset. Note that `WithOTLP(&OTLPConfig{...})` states every transport field of the struct (endpoint, insecure, headers, timeout, compression, protocol, temporality), so a zero value in it also beats the environment. To take transport settings from the environment, use `WithOTLPFromEnv()` and override single settings with `WithOTLPExportTimeout`, `WithTemporality` and the like.

Supported variables:

| Variable | Effect |
|---|---|
| `OTEL_SDK_DISABLED` | `true` (case-insensitive) disables the client, see below; any other value leaves it enabled |
| `OTEL_SERVICE_NAME` | default service name; `WithServiceName` wins |
| `OTEL_RESOURCE_ATTRIBUTES` | resource attributes, percent-decoded |
| `DEPLOYMENT_ENVIRONMENT`, `SERVICE_VERSION` | `deployment.environment` and `service.version` of the OTLP resource |
| `OTEL_EXPORTER_OTLP_PROTOCOL`, `OTEL_EXPORTER_OTLP_METRICS_PROTOCOL` | `grpc` or `http/protobuf`; `http/json` is rejected |
| `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` | collector endpoint. The generic one gets `/v1/metrics` appended for HTTP; the metrics one is used as is. An `http://` URL implies an insecure connection |
| `OTEL_EXPORTER_OTLP_INSECURE`, `OTEL_EXPORTER_OTLP_METRICS_INSECURE` | `true` or `false` |
| `OTEL_EXPORTER_OTLP_HEADERS`, `OTEL_EXPORTER_OTLP_METRICS_HEADERS` | `key=value,key2=value2`, keys and values percent-decoded |
| `OTEL_EXPORTER_OTLP_TIMEOUT`, `OTEL_EXPORTER_OTLP_METRICS_TIMEOUT` | per-export deadline, a positive whole number of milliseconds |
| `OTEL_EXPORTER_OTLP_COMPRESSION`, `OTEL_EXPORTER_OTLP_METRICS_COMPRESSION` | `gzip` or `none` |
| `OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE` | `cumulative` or `delta`; `lowmemory` is rejected |
| `OTEL_METRIC_EXPORT_INTERVAL` | pipeline flush interval, a positive whole number of milliseconds; `WithFlushInterval` and `WithOTLPExportInterval` win |
| `STATS_DISABLE_GO_VERSION_REPORTING` | `true`, `TRUE`, `yes` or `1` turns off the version gauges; `WithVersionReporting` wins |

When OTLP is enabled, a malformed value of a supported `OTEL_EXPORTER_OTLP_*` or `OTEL_METRIC_EXPORT_INTERVAL` variable makes `NewClient` return an error wrapping `ErrInvalidConfig` that names the variable. A setting stated through an option is not read from the environment, so a bad variable cannot break a caller that overrode it.

Not supported, with no effect when set: `OTEL_EXPORTER_OTLP_CERTIFICATE`, the `OTEL_EXPORTER_OTLP_CLIENT_*` variables, `OTEL_EXPORTER_OTLP_METRICS_DEFAULT_HISTOGRAM_AGGREGATION` (use `WithExponentialHistogram`), and every other `OTEL_*` variable not listed above (for example `OTEL_METRICS_EXPORTER` and `OTEL_METRIC_EXPORT_TIMEOUT`). The library also passes the resolved endpoint, TLS mode, headers, timeout and compression to the SDK exporters explicitly, so a stray SDK variable cannot change them.

### OTEL_SDK_DISABLED

When `OTEL_SDK_DISABLED` is `true`, `NewClient` and `otel.NewMeterProvider` start no pipeline, exporter or runtime collector and dial nothing. Every recording method (and views from `WithPrefix`/`WithTags`) does nothing and returns nil without validating input. `Flush`, `Shutdown` and `Close` return nil, `Stats()` returns a zero `ClientStats`, observable callbacks are never registered, and `client.Disabled()` reports the state.

## Instrumentation and test packages

| Package | Purpose |
|---|---|
| `httpstats` | server middleware and client transport with the standard OTel HTTP metric names; `http.route` is the route template and is omitted when unset, and paths and URLs are never recorded; also header count and size histograms and an error counter, and content attributes with `httpstats.WithContentAttributes()` |
| `netstats` | `net.Conn`, `net.Listener` and connection-handler wrappers (`conn.*` metrics). Unlike segmentio, read and write totals are batched and flushed on close and every 10 s, not recorded per call |
| `iostats` | `CountReader`, `CountWriter`, `ReaderFunc`, `WriterFunc`, `CloserFunc` |
| `statstest` | `NewClient(t)` with a capturing `Exporter` (`Metrics`, `Clear`, `FlushCalls`), `Flush(t, client)`, and `DogStatsDServer` / `NewDogStatsDServer(t, handler)` to assert on the bytes a client sends. A `DogStatsDHandler` has `HandleMetric(DogStatsDMetric, from)` and `HandleEvent(DogStatsDEvent, from)`; `DogStatsDHandlerFunc` receives a `DogStatsDMessage`. Metric types are `DogStatsDMetricType` values (`DogStatsDCounter`, `DogStatsDGauge`, `DogStatsDHistogram`, `DogStatsDDistribution`, `DogStatsDSet`, `DogStatsDTiming`); `statstest.NewExporter()` is the bare capturing `Exporter` |
| `debugstats` | `Exporter{Dst, Grep}` prints every metric as one StatsD-format line |

`httpstats` and `netstats` also have constructors without a recorder argument (`NewHandler`, `NewTransport`, `NewConn`, `NewListener`) that record to a package default set with `SetDefaultRecorder`; they record nothing until it is set. Wrapped connections must be closed, and an `httpstats` client response body must be closed or read to EOF, because that is when the totals and the duration are recorded. Two `debugstats.Exporter` values cannot share a client, since exporter names must be unique.

## Run it end to end

[examples/docker](../examples/docker/README.md) starts a demo service, an OpenTelemetry Collector and Prometheus with `docker compose up --build`, and shows the `_bucket` series.

## More

- [README](../README.md): backends, OTel API mode, runtime metrics, instrumentation packages and the [migration table from segmentio/stats](../README.md#migrating-from-segmentiostats).
- [docs/runtime_metrics.md](runtime_metrics.md): runtime, memstats and process metrics.
- [docs/otel_compliance.md](otel_compliance.md): OTLP semantics, SemVer exception and known limitations.
- [CHANGELOG](../CHANGELOG.md).
