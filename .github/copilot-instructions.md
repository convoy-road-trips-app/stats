# Copilot Instructions — `github.com/convoy-road-trips-app/stats`

Non-blocking metrics library: Legacy API (`stats.NewClient`) and OTel API (`otel.NewMeterProvider`) share one pipeline:
ring buffer → worker pool → parallel exporters (Datadog/Prometheus StatsD over UDP, CloudWatch EMF, OTLP).
See `CLAUDE.md` and `docs/architecture.md` for the full picture.

## Package invariants (must never regress)

1. **Hot path never blocks.** `Counter/Gauge/Histogram/Timing` and OTel `Add/Record` must not take locks, do I/O, wait on channels, or allocate without pooling. On overflow, drop per `DropNewest`/`DropOldest` and count the drop. Never return an error that forces the caller to handle backpressure.
2. **Ring buffer is lock-free** (`transport/buffer.go`): atomic CAS only, power-of-2 capacity, cache-line padding preserved. No `sync.Mutex` here.
3. **Exporter isolation.** Each exporter runs concurrently and has its own failure domain: a slow, failing, or panicking exporter must not stall workers or other exporters. Panics are recovered in workers; errors are counted per exporter (`Stats().Pipeline.ExporterErrors`).
4. **Circuit breaker** (`transport/circuit.go`): Closed/Open/Half-Open transitions stay race-free and time-driven through the injectable clock (`clock.go`), not `time.Now()` directly.
5. **Pooling discipline.** Objects from `sync.Pool` are reset before reuse and never used after `Put`. Slices/maps passed in by callers (attributes, histogram buckets) are copied, not aliased.
6. **Lifecycle.** `Close`/`Shutdown`/`Flush` are idempotent, respect `context` deadlines, drain the buffer (`drain.go`, flush barrier), and leave no goroutines behind. Calls after close are safe no-ops.
7. **Cardinality and rate limits** (`cardinality.go`, `ratelimit.go`) are enforced before metrics reach exporters; attribute keys are canonicalized consistently.
8. **Dual-mode parity.** A change to pipeline/exporter behavior must work identically for the Legacy and OTel APIs. OTel async/observable instruments are intentionally unsupported (see `docs/otel_compliance.md`).
9. **Dependency direction:** `models`/`internal/types` ← `serializers` ← `exporters` ← root `stats` ← `otel`. Never import the root package from `transport`, `serializers`, `exporters`, or `models`. `vendor/` is committed, so add dependencies only when necessary and re-vendor (`go mod vendor`).

## Code review: focus on the diff

Review **only the changed lines and what they directly affect**. Do not comment on untouched code unless the change makes it wrong. For each finding, give the file and line, the concrete failure scenario, and a minimal fix. Leave out style nitpicks that `gofmt`/`golangci-lint` already catch.

### Follow-up reviews: check prior comments first
Before reviewing new changes, go through every earlier review comment on the PR/thread (including your own) and give each one a status:
- **Fixed**: name the commit or line that resolves it. Confirm the fix addresses the root cause, not just the symptom, and has a test where one was asked for.
- **Partially fixed**: say what is still missing.
- **Not fixed**: carry it forward and keep its original severity.
- **Regressed / fix introduced a new issue**: report it as a new finding.
- **Won't fix / disputed**: note the author's reasoning, and say whether you accept it.

Start the review with this status table, then list only **new** findings in the delta. Do not re-raise comments that are resolved.

Rank findings: **Blocker** (correctness, races, invariant break, API break) → **Major** (perf regression on hot path, leak, missing test) → **Minor**.

### Go robustness checklist (apply to the delta)
- **Concurrency:** shared state behind atomics or a lock, never mixed. No copying of structs that contain `sync`/atomic fields. Every new goroutine has a clear exit through ctx/close channel. No send on a possibly-closed channel. `select` includes `ctx.Done()`.
- **Errors:** wrapped with `%w` and sentinel errors from `errors.go`. No silently dropped errors except deliberate drops, which must be counted. No `panic` in library code paths.
- **Context and time:** respect ctx cancellation and deadlines. UDP writes keep write deadlines. Use the clock abstraction for testable timing.
- **Allocations on the hot path:** no `fmt.Sprintf`, closures capturing per-call values, interface boxing, or map/slice growth per metric. Prove claims with `-benchmem`.
- **Resource safety:** connections, timers, and tickers are closed or stopped. `defer` is not used inside hot loops.
- **API surface:** exported names, functional options, and defaults stay backward compatible (semver v1). New options get validation and a test in `options_test.go`. Update `CHANGELOG.md` for user-visible changes.
- **Tests:** new behavior has a focused test. Concurrency changes have a `-race` test. Timing tests use the fake clock and do not use `time.Sleep`.

### Architecture checklist (apply to the delta)
- Does the change preserve invariants 1–9 above? Name the invariant if it does not.
- Is the logic in the right layer (serialization in `serializers`, wire/transport in `transport`, backend specifics in `exporters/<backend>`)?
- Does it add coupling between exporters or between the Legacy and OTel paths?
- Is a new abstraction justified by more than one caller? Prefer duplication to premature abstraction.

## Focused test commands

```bash
go test ./transport/...                      # ring buffer, UDP pool, circuit breaker
go test -race -run 'TestRingBuffer|TestCircuit' ./transport/...
go test -race -run 'Pipeline|Backpressure|DropOldest|Flush' .   # pipeline & lifecycle
go test -run 'Cardinality' .                 # cardinality limits
go test ./otel/...                           # OTel compliance
go test ./exporters/<backend>/... ./serializers/...
go test -bench=. -benchmem ./transport/...   # hot-path perf
make test-race                               # full suite with race detector
make lint                                    # go vet + golangci-lint
make integration-test                        # Docker backends (-tags=integration ./test/integration)
```

Run the narrowest package and `-run` filter that covers the change first, then run `make test-race` before you finish.
