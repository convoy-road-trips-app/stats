# Runtime Metrics

Automatic Go runtime telemetry collection for CPU, heap, GC, goroutine, memstats-style and (opt-in) process and delay metrics.

## Overview

The runtime metrics collector periodically samples Go's `runtime/metrics` package and pushes the values as gauge metrics through the existing stats pipeline. It works identically in both Legacy and OTel modes.

**Key properties:**
- Opt-in via `stats.WithRuntimeMetrics()`
- Non-blocking: uses the same non-blocking pipeline as application metrics
- No STW: uses `runtime/metrics` (semaphore lock), NOT `runtime.ReadMemStats` (stops the world)
- Low overhead: sample slice allocated once and reused across ticks
- Default collection interval: 10 seconds
- Default metric prefix: `runtime.go`
- Process metrics (`stats.WithRuntimeProcessMetrics()`) and delay metrics (`stats.WithRuntimeDelayMetrics()`) are separate opt-ins, described below
- Collector failures are counted in `ExporterErrors["runtimemetrics.<source>"]`, at most once per source

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

The `memory.*` names follow Go's `runtime.MemStats` fields (heap, stack, mspan, mcache, buckhash, gc and other off-heap bytes). They are derived from `runtime/metrics` samples, never from `runtime.ReadMemStats`, so collection never stops the world. A metric whose source is missing on the running Go version is skipped.

| Metric Name | Source | Description |
|---|---|---|
| `runtime.go.memory.heap.alloc` | `/memory/classes/heap/objects:bytes` | Bytes of allocated heap objects |
| `runtime.go.memory.heap.inuse` | `/memory/classes/heap/objects:bytes` + `/memory/classes/heap/unused:bytes` | Bytes in in-use heap spans |
| `runtime.go.memory.heap.idle` | `/memory/classes/heap/free:bytes` + `/memory/classes/heap/released:bytes` | Bytes in idle heap spans |
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
| `runtime.go.memory.alloc` | `/memory/classes/heap/objects:bytes` | Total bytes of allocated objects (`MemStats.Alloc`, segmentio `alloc.bytes{type=total}`). The Go runtime defines it as `HeapAlloc`, so it equals `memory.heap.alloc`; it is a separate series for dashboards that query it by its segmentio role |
| `runtime.go.memory.heap.sys` | `heap/objects` + `heap/unused` + `heap/free` + `heap/released` | Heap bytes obtained from the OS (`MemStats.HeapSys`, segmentio `sys.bytes{type=heap}`) |

`memory.sys` is the total bytes obtained from the OS (`MemStats.Sys`, segmentio `sys.bytes{type=total}`).

**Not provided:**

- `MemStats.Lookups` (segmentio `lookups.count`): the Go runtime never writes this field (it is only declared and dumped by the heap dumper) and `runtime/metrics` has no equivalent, so it would always be zero. It is not emitted.
- `MemStats.TotalAlloc`, `Mallocs`, `Frees`, `NumGC` and `HeapObjects` are covered by the cumulative `heap.allocs.bytes`, `heap.allocs.objects`, `heap.frees.objects`, `gc.cycles.total` and `heap.objects.live`. They are absolute gauges here, where segmentio sends per-interval deltas.

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
| `runtime.go.cpu.num` | `runtime.NumCPU()` | Logical CPUs usable by the process (segmentio `cpu.num`) |
| `runtime.go.cpu.physical.num` | `/proc/cpuinfo` (Linux), `hw.physicalcpu` (Darwin) | Physical cores. Not emitted when the platform does not expose it (other platforms, and Linux kernels whose `/proc/cpuinfo` has no `physical id`/`core id`, such as most ARM) |

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
| `runtime.go.cpu.usage_total.seconds` | | `/proc/self/stat` | User plus system CPU time (segmentio `usage_total.seconds`) |
| `runtime.go.cpu.usage_user.percent`, `cpu.usage_system.percent`, `cpu.usage_total.percent` | | derived | Δ user, system, or total CPU seconds / Δwall / **CPU capacity** × 100. The capacity is the cgroup quota (`quota / period` cores) when one is set and GOMAXPROCS otherwise, so a container limited to 1.5 cores reports 100 at 1.5 busy cores. Not emitted on the first sample. Separate names rather than a `type` attribute on `cpu.usage.percent`, so summing the existing series is unchanged |
| `runtime.go.cpu.cgroup.quota.seconds`, `cpu.cgroup.period.seconds` | | cgroup v2 `cpu.max`; v1 `cpu.cfs_quota_us`, `cpu.cfs_period_us` | CPU time allowed per period, and the period. No quota series when unlimited. Read from the cgroup in `/proc/self/cgroup` (the mount root in a private cgroup namespace) |
| `runtime.go.cpu.cgroup.weight` | | cgroup v2 `cpu.weight` | Relative CPU weight, 1-10000 (default 100) |
| `runtime.go.cpu.cgroup.shares` | | cgroup v1 `cpu.shares` | Relative CPU weight (default 1024) |
| `runtime.go.memory.usage.percent` | `type=resident` | derived | Resident set size as a percentage of `memory.total.bytes` (host memory, capped by the cgroup limit). segmentio divides by available memory instead; capacity is used here so the value does not rise when the host frees cache |
| `runtime.go.memory.virtual.bytes` | | `/proc/self/statm` size × page size | Virtual size of the process including mappings. Distinct from `memory.total.bytes`, which is the host or cgroup capacity |
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
| `runtime.go.cpu.usage_total.seconds` | | derived | User plus system CPU time |
| `runtime.go.cpu.usage_user.percent`, `cpu.usage_system.percent`, `cpu.usage_total.percent` | | derived | Same formulas as Linux with GOMAXPROCS as capacity; not emitted on the first sample |
| `runtime.go.memory.usage.bytes` | `type=resident` | `ru_maxrss` | **Peak** resident size, not current. Darwin reports it in **bytes**; Linux `VmRSS` is read in kilobytes and converted to bytes, so both platforms emit bytes |
| `runtime.go.memory.pagefault.count` | `type=major\|minor` | `ru_majflt`, `ru_minflt` | Cumulative |
| `runtime.go.threads.switch.count` | `type=voluntary\|involuntary` | `ru_nvcsw`, `ru_nivcsw` | Cumulative |

Not emitted on Darwin: `memory.usage.bytes` for `shared`, `text` and `data`, `memory.usage.percent`, `memory.virtual.bytes`, `memory.available.bytes`, `memory.total.bytes`, `files.open.count`, `files.open.max`, `threads.count` and the `cpu.cgroup.*` series. rusage has no current or virtual size and no host memory, and Darwin has no cgroups. A failing `getrusage` call is skipped and reported once as `OnError("process", err)`.

A source that cannot be read is skipped for that collection and reported through `OnError("process", err)` at most once per source (surfacing as `ExporterErrors["runtimemetrics.process"]`).

### Delay Metrics (Linux, opt-in)

Enabled with `stats.WithRuntimeDelayMetrics()` (implies `WithRuntimeMetrics()`). They come from the Linux taskstats netlink interface for the current process, so they need **Linux, `CAP_NET_ADMIN` (or root) and kernel delay accounting enabled** (`CONFIG_TASK_DELAY_ACCT`, `sysctl kernel.task_delayacct=1` on kernels that default it off). These are **counters**: the kernel totals are cumulative, so each collection emits the increase since the previous one (the first collection emits the full total). A total that decreases is treated as a reset and emitted as-is; increments are never negative.

| Metric Name | Description |
|---|---|
| `runtime.go.cpu.delay.seconds` | Time runnable but waiting for a CPU |
| `runtime.go.blockio.delay.seconds` | Time waiting for synchronous block I/O |
| `runtime.go.swapin.delay.seconds` | Time waiting for swap-in |
| `runtime.go.freepages.delay.seconds` | Time waiting for memory reclaim (zero on kernels without the field) |

If the first read fails (unsupported platform, `EPERM`, delay accounting off), the error is reported through `OnError("delay", err)` exactly once (surfacing as `ExporterErrors["runtimemetrics.delay"]`) and delay collection is disabled for the life of the client; it is never retried.

## The runtimemetrics package

`stats.WithRuntimeMetrics()` wires a `runtimemetrics.Collector` into the client. You can also use the package directly:

- `runtimemetrics.New(cfg Config, record RecordFunc) *Collector` with `Start`, `Collect` (one synchronous sample) and `Stop(ctx)`. `Config` has `CollectInterval`, `Prefix`, `ProcessMetrics`, `DelayMetrics` and `OnError func(source string, err error)`. The package never imports `stats`; `RecordFunc` is how metrics leave it.
- `runtimemetrics.Get(pid int) (DelayInfo, error)` reads the cumulative taskstats delay totals of a process (`DelayInfo{CPU, BlockIO, SwapIn, FreePages time.Duration}`). On platforms other than Linux it returns an error for which `runtimemetrics.IsUnsupported(err)` is true.
- `runtimemetrics.ParseTaskstatsReply(buf, seq)` decodes a netlink reply to a taskstats request and never panics on malformed input.

### Other processes, composition and procfs readers

- `runtimemetrics.CollectProcInfo(pid int) (ProcInfo, error)` returns a snapshot (`ProcInfo{CPU, Memory, Files, Threads}`, the shape of segmentio's `procstats.ProcInfo`) of any process, not just the caller. On Linux it reads `/proc/<pid>` and the process's cgroup; `stat`, `statm` and `status` are required, while limits, fd count, meminfo and cgroup are best effort (zero when unreadable, for example another user's `/proc/<pid>/fd`). On Darwin only the current process can be read, from `getrusage` and `getrlimit`; on other platforms it always fails. In both failing cases `runtimemetrics.IsUnsupported(err)` is true. `ProcInfo.Memory.Total` is the cgroup or host capacity and `Memory.Available` the host `MemAvailable`; `Files.Max` is zero when unlimited.
- `runtimemetrics/procfs` exposes read-only parsers and readers for the Linux files behind the process metrics: `ParseStat`, `ParseStatm`, `ParseSched`, `ParseLimits`, `ParseStatus`, `ParseMeminfo`, `ParseCGroups`, `ParseCPUMax`, `ParseMemoryLimit` and `KeyValues`, each with a `Reader` method (`ReadStat(pid)`, `ReadCPUConfig(pid)`, `ReadMemoryLimit(pid)`, `OpenFileCount(pid)`, ...) and a package-level function that uses `/proc` and `/sys/fs/cgroup`. A `procfs.Reader{ProcRoot, CgroupRoot}` reads other roots, such as a host procfs mounted into a container. The parsers build and run on every platform and never panic on bad input (errors wrap `procfs.ErrMalformed`). The self-process collector uses the same parsers; its metric names are unchanged. The package depends only on the standard library.
- `runtimemetrics.MetricCollector` (`Collect(record RecordFunc)`), `CollectorFunc`, `MultiCollector(...)`, `MultiCollectorWith(onError, ...)`, `StartCollector(c, record)` and `StartCollectorWith(ScheduleConfig{Collector, CollectInterval, OnError}, record)` mirror segmentio's `Collector`, `CollectorFunc`, `MultiCollector` and `StartCollector`, adapted to `RecordFunc`: a collector emits through the record function it is given instead of a global engine, so pass a function that forwards to your client. `StartCollector` collects at once and then every 15 seconds (`DefaultCollectInterval`), returns an `io.Closer` that stops the schedule and waits for an in-flight collection, and recovers a panicking collector (reported through `OnError`) so the schedule keeps running. `AsMetricCollector(c *Collector)` lets the built-in collector take part in a `MultiCollector`. The interface is named `MetricCollector` because `Collector` is already the built-in runtime collector type.

## Semantics: Why Gauges, Not Counters

Several runtime values (CPU seconds, alloc bytes, GC cycles) are **cumulative since process start**. We emit them as absolute gauges rather than delta counters because (the delay metrics above are the exception, because the kernel totals are read from outside the Go runtime and are emitted as increments):

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
