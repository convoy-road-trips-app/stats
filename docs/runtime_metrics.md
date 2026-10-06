# Runtime Metrics

Automatic Go runtime telemetry collection for CPU, heap, GC, and goroutine metrics.

## Overview

The runtime metrics collector periodically samples Go's `runtime/metrics` package and pushes the values as gauge metrics through the existing stats pipeline. It works identically in both Legacy and OTel modes.

**Key properties:**
- Opt-in via `stats.WithRuntimeMetrics()`
- Non-blocking: uses the same non-blocking pipeline as application metrics
- No STW: uses `runtime/metrics` (semaphore lock), NOT `runtime.ReadMemStats` (stops the world)
- Low overhead: sample slice allocated once and reused across ticks
- Default collection interval: 10 seconds
- Default metric prefix: `runtime.go`

## Enabling

### Legacy Mode

```go
client, err := stats.NewClient(
    stats.WithServiceName("my-service"),
    stats.WithRuntimeMetrics(),
)
defer client.Close()
```

### OTel Mode

```go
provider, err := otel.NewMeterProvider(
    otel.WithStatsOptions(
        stats.WithServiceName("my-service"),
        stats.WithRuntimeMetrics(),
    ),
)
defer provider.Shutdown(context.Background())
```

## Metric Reference

All metrics are emitted as **gauges with absolute values**. The default prefix is `runtime.go`.

### Memory

| Metric Name | Source | Description |
|---|---|---|
| `runtime.go.memory.heap.alloc` | `/memory/classes/heap/objects:bytes` | Bytes of allocated heap objects |
| `runtime.go.memory.heap.inuse` | `/memory/classes/heap/inuse:bytes` | Bytes in in-use heap spans |
| `runtime.go.memory.heap.idle` | `/memory/classes/heap/idle:bytes` | Bytes in idle heap spans |
| `runtime.go.memory.heap.released` | `/memory/classes/heap/released:bytes` | Bytes released to the OS |
| `runtime.go.memory.sys` | `/memory/classes/total:bytes` | Total bytes obtained from OS |
| `runtime.go.memory.stack.inuse` | `/memory/classes/heap/stacks:bytes` | Bytes in stack spans |
| `runtime.go.memory.stack.sys` | `heap/stacks` + `os-stacks` | Stack bytes obtained from the OS |
| `runtime.go.memory.mspan.inuse` | `/memory/classes/metadata/mspan/inuse:bytes` | Bytes of in-use mspan structures |
| `runtime.go.memory.mspan.sys` | `mspan/inuse` + `mspan/free` | mspan bytes obtained from the OS |
| `runtime.go.memory.mcache.inuse` | `/memory/classes/metadata/mcache/inuse:bytes` | Bytes of in-use mcache structures |
| `runtime.go.memory.mcache.sys` | `mcache/inuse` + `mcache/free` | mcache bytes obtained from the OS |
| `runtime.go.memory.buckhash.sys` | `/memory/classes/profiling/buckets:bytes` | Profiling bucket hash table bytes |
| `runtime.go.memory.gc.sys` | `/memory/classes/metadata/other:bytes` | GC metadata bytes |
| `runtime.go.memory.other.sys` | `/memory/classes/other:bytes` | Other off-heap runtime bytes |

### Heap Allocations

| Metric Name | Source | Description |
|---|---|---|
| `runtime.go.heap.allocs.bytes` | `/gc/heap/allocs:bytes` | Cumulative bytes allocated |
| `runtime.go.heap.frees.bytes` | `/gc/heap/frees:bytes` | Cumulative bytes freed |
| `runtime.go.heap.allocs.objects` | `/gc/heap/allocs:objects` | Cumulative objects allocated |
| `runtime.go.heap.frees.objects` | `/gc/heap/frees:objects` | Cumulative objects freed |
| `runtime.go.heap.objects.live` | `/gc/heap/objects:objects` | Currently live heap objects |
| `runtime.go.heap.goal.bytes` | `/gc/heap/goal:bytes` | Target heap size for next GC |

### Garbage Collection

| Metric Name | Source | Description |
|---|---|---|
| `runtime.go.gc.cycles.total` | `/gc/cycles/total:gc-cycles` | Total completed GC cycles |
| `runtime.go.gc.cpu.seconds` | `/cpu/classes/gc/total:cpu-seconds` | CPU time spent in GC |
| `runtime.go.gc.next.bytes` | `/gc/heap/goal:bytes` | Target heap size for next GC (alias of `heap.goal.bytes`) |
| `runtime.go.gc.cpu.fraction` | `/cpu/classes/gc/total` / `/cpu/classes/total` | Fraction of CPU time used by GC since process start; `0` until the runtime has accounted CPU time |
| `runtime.go.gc.pause.seconds.min` | `/gc/pauses:seconds` delta | Lower bound of the lowest non-empty pause bucket since the previous collect (approximation) |
| `runtime.go.gc.pause.seconds.max` | `/gc/pauses:seconds` delta | Upper bound of the highest non-empty pause bucket since the previous collect (approximation) |
| `runtime.go.gc.pause.seconds.avg` | `/gc/pauses:seconds` delta | Count-weighted bucket-midpoint mean pause since the previous collect (approximation) |

The `gc.pause.seconds.*` gauges are computed from the difference between the current and previous `/gc/pauses:seconds` histogram snapshots. They are **not emitted** (rather than reported as NaN) when no GC pause occurred since the previous collect. `min`/`max` are histogram bucket bounds, not exact pause durations. Metrics whose `runtime/metrics` source is missing on the running Go version are skipped.

### Scheduler

| Metric Name | Source | Description |
|---|---|---|
| `runtime.go.goroutines` | `/sched/goroutines:goroutines` | Current goroutine count |
| `runtime.go.gomaxprocs` | `runtime.GOMAXPROCS(0)` | Current GOMAXPROCS value |
| `runtime.go.cgo.calls` | `/cgo/go-to-c-calls:calls` | Cumulative cgo calls |

### CPU Time

| Metric Name | Source | Description |
|---|---|---|
| `runtime.go.cpu.total.seconds` | `/cpu/classes/total:cpu-seconds` | Total CPU time consumed |
| `runtime.go.cpu.user.seconds` | `/cpu/classes/user:cpu-seconds` | User-space CPU time |
| `runtime.go.cpu.gc.seconds` | `/cpu/classes/gc/total:cpu-seconds` | GC CPU time |
| `runtime.go.cpu.idle.seconds` | `/cpu/classes/idle:cpu-seconds` | Idle CPU time |
| `runtime.go.cpu.scavenge.seconds` | `/cpu/classes/scavenge/total:cpu-seconds` | Scavenger CPU time |

### Process Metrics (Linux and Darwin, opt-in)

The table below describes Linux; see [Darwin](#darwin) for macOS. Enabled with `stats.WithRuntimeProcessMetrics()` (implies `WithRuntimeMetrics()`). Collected on Linux and Darwin; other platforms (including Windows) emit nothing. All are gauges under the runtime prefix (cumulative values are absolute, see below).

| Metric Name | Attributes | Source | Description |
|---|---|---|---|
| `runtime.go.cpu.usage.seconds` | `type=user\|system` | `/proc/self/stat` utime/stime | Process CPU time. Ticks are converted at a constant 100 Hz (USER_HZ) |
| `runtime.go.cpu.usage.percent` | | derived | Δcpu seconds / Δwall seconds / GOMAXPROCS × 100; not emitted on the first sample |
| `runtime.go.memory.usage.bytes` | `type=resident\|shared\|text\|data` | `/proc/self/status` | VmRSS; RssFile+RssShmem; VmExe; VmData |
| `runtime.go.memory.available.bytes` | | `/proc/meminfo` | MemAvailable |
| `runtime.go.memory.total.bytes` | | `/proc/meminfo` | MemTotal, capped by cgroup v2 `/sys/fs/cgroup/memory.max` when numeric |
| `runtime.go.memory.pagefault.count` | `type=major\|minor` | `/proc/self/stat` | Cumulative page faults |
| `runtime.go.files.open.count` | | `/proc/self/fd` | Open file descriptors |
| `runtime.go.files.open.max` | | `/proc/self/limits` | Soft "Max open files" limit; not emitted when unlimited |
| `runtime.go.threads.count` | | `/proc/self/stat` num_threads | OS threads |
| `runtime.go.threads.switch.count` | `type=voluntary\|involuntary` | `/proc/self/status` | Cumulative context switches |

#### Darwin

On Darwin the metrics come from `getrusage(RUSAGE_SELF)` through the standard library (no cgo). Only what rusage provides is emitted, with the same names and attributes as Linux:

| Metric Name | Attributes | rusage field | Notes |
|---|---|---|---|
| `runtime.go.cpu.usage.seconds` | `type=user\|system` | `ru_utime`, `ru_stime` | |
| `runtime.go.cpu.usage.percent` | | derived | Same formula as Linux; not emitted on the first sample |
| `runtime.go.memory.usage.bytes` | `type=resident` | `ru_maxrss` | **Peak** resident size, not current. Darwin reports it in **bytes**; Linux `VmRSS` is read in kilobytes and converted to bytes, so both platforms emit bytes |
| `runtime.go.memory.pagefault.count` | `type=major\|minor` | `ru_majflt`, `ru_minflt` | Cumulative |
| `runtime.go.threads.switch.count` | `type=voluntary\|involuntary` | `ru_nvcsw`, `ru_nivcsw` | Cumulative |

Not emitted on Darwin: `memory.usage.bytes` for `shared`, `text` and `data`, `memory.available.bytes`, `memory.total.bytes`, `files.open.count`, `files.open.max` and `threads.count`. A failing `getrusage` call is skipped and reported once as `OnError("process", err)`.

A source that cannot be read is skipped for that collection and reported through `OnError("process", err)` at most once per source (surfacing as `ExporterErrors["runtimemetrics.process"]`).

## Semantics: Why Gauges, Not Counters

Several runtime values (CPU seconds, alloc bytes, GC cycles) are **cumulative since process start**. We emit them as absolute gauges rather than delta counters because:

1. **No bootstrap spike**: A counter would emit a massive value on the first sample (all time since process start). Gauges avoid this entirely.
2. **Restart-safe**: When a process restarts, the gauge starts from zero naturally. Delta counters would require tracking previous values across restarts.
3. **No state**: The collector doesn't need to store previous sample values to compute deltas.
4. **Backend-friendly**: All major backends support computing rates from monotonically-increasing gauges:

### Computing Rates in Backends

**Datadog:**
```
rate(runtime.go.cpu.user.seconds{service:my-app})
```

**Prometheus (PromQL):**
```
rate(stats_runtime_go_cpu_user_seconds[5m])
```

**Grafana:**
```
increase(stats_runtime_go_heap_allocs_bytes[$__interval])
```

Note: The `stats_` prefix comes from the Prometheus exporter's configured Job name (default `"stats"`). Adjust to match your `PrometheusConfig.Job` value.

## v1 Scope

### Included
- All scalar metrics from `runtime/metrics` (Uint64, Float64)
- `runtime.GOMAXPROCS(0)` as a direct scalar

### Excluded (future work)
- **Full GC pause histogram** (`/gc/pauses:seconds`) emission: only per-interval min/max/avg summaries are emitted (see above).
- **Observable/async OTel instruments**: Not supported by the library's OTel implementation.

## Performance

- `metrics.Read()` acquires a semaphore (not STW) — safe to call frequently
- The `[]metrics.Sample` slice is allocated once at collector creation and reused every tick
- Bucket boundaries for histogram metrics (if added in the future) are stable until process exit — can be cached
- Default 10s interval adds ~20 gauge metrics per tick — negligible pipeline load
