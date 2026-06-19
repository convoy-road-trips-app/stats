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
rate(runtime_go_cpu_user_seconds[5m])
```

**Grafana:**
```
increase(runtime_go_heap_allocs_bytes[$__interval])
```

## v1 Scope

### Included
- All scalar metrics from `runtime/metrics` (Uint64, Float64)
- `runtime.GOMAXPROCS(0)` as a direct scalar

### Excluded (future work)
- **GC pause histogram** (`/gc/pauses:seconds`): This is a `Float64Histogram` with cumulative bucket counts. Representing it cleanly requires multi-bucket emission or per-interval diffing, which adds complexity. Deferred to a future version.
- **Observable/async OTel instruments**: Not supported by the library's OTel implementation.

## Performance

- `metrics.Read()` acquires a semaphore (not STW) — safe to call frequently
- The `[]metrics.Sample` slice is allocated once at collector creation and reused every tick
- Bucket boundaries for histogram metrics (if added in the future) are stable until process exit — can be cached
- Default 10s interval adds ~20 gauge metrics per tick — negligible pipeline load
