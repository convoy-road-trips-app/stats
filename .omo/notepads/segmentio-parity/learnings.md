# Learnings — segmentio-parity

Conventions, patterns, and successful approaches discovered during work on this plan.

---

## PR27 (test: shared per-metric buckets across exporters)
- A root-package test can exercise one real OTLP/HTTP receiver and a Prometheus pull Handler via one client, asserting bounds from both exported surfaces for named, global fallback, and default fallback cases.
- `attribute.NewSet` sorts its variadic attribute slice in place. Exporters run concurrently on shared metrics, so clone metric attributes before creating an OTel set in both the OTLP and Prometheus exporters to prevent a data race and mutation of pipeline-owned metrics.
- Verification: the focused shared-bucket test, requested lint/vet/race checks, module diff, and darwin/linux/windows builds passed; see `.omo/evidence/segmentio-parity/pr27.txt`.
- The task test uses the real client, OTLP/HTTP protobuf receiver, and Prometheus scrape endpoint; named, global fallback, and default fallback bounds are asserted on both surfaces.

## PR15c (feat: WithExponentialHistogram for OTLP)
- `WithExponentialHistogram(maxSize, maxScale)` follows segmentio's SDKConfig: a zero argument selects the default (160 buckets, scale 20), so scale 0 cannot be chosen; this is documented on the option and on `models.OTLPExponentialHistogram`. Defaults are resolved by the non-mutating `Resolved()`, so a struct passed to `WithOTLP` (which now copies it) is never changed.
- `ValidateConfig` wraps OTLP errors as `"otlp config: %w"` without `ErrInvalidConfig`. To return `ErrInvalidConfig` for the new setting, even when OTLP is not enabled, `validateOTLPConfig` checks it first; extracting that helper also cut `ValidateConfig`'s cyclomatic complexity from 29 to 22.
- Cumulative exponential state is a deep copy (`copyExponential`), and idle re-exports copy it again: the OTLP transform puts our bucket count slices straight into the protobuf message. The test overwrites bucket counts in the collected exports and checks that the next cumulative point is unaffected; removing either clone makes it fail.
- `sameKind` in reexport.go must handle every aggregation type, otherwise `appendUnobserved` adds a second metric of the same name for idle series instead of joining the observed one.
- Merging uses `mergeExpo(expoHistogramFromPoint(previous, maxSize), expoHistogramFromPoint(point, maxSize))`, so the accumulator needs the resolved maxSize (`accumulation.expoMaxSize`); the datapoint does not carry it.
- `go test -run Expo` also runs every `TestExporter*` test ("Exporter" contains "Expo"); filter output with `TestExpo[A-Z]`.
- Lint: the `golangci-lint` on PATH is v1.64.8 and rejects the v2 config, so `make lint` fails on main too. `GOBIN=<tmp> go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2` works offline from the module cache. Full v2 runs report 49 issues that already exist on main; CI uses only-new-issues, i.e. `--new-from-rev=main`. Give each worktree its own `GOLANGCI_LINT_CACHE`: a shared cache reported files of other worktrees. With the default `max-same-issues: 3`, same-text issues (intrange) vary between runs, so compare main and branch with `--max-same-issues=0 --max-issues-per-linter=0`.
- Lint gotchas hit here: `unparam` flags test helpers whose name parameter always receives the same literal (name the helper after the metric instead); `prealloc` flags `append(small, large...)` on a slice literal (use `slices.Concat`); `metricdata.ExponentialHistogramDataPoint` is ~250 bytes, so pass it by pointer (gocritic hugeParam) and index its slices instead of ranging by value (rangeValCopy).
- Adding exponential conversion took `exporters/otlp/exporter.go` to 246 pure LOC, so the unchanged explicit-bucket aggregation moved to `histogram.go`, next to `histogram_test.go`. Evidence: `.omo/evidence/segmentio-parity/10-happy.txt` and `10-fail.txt`.

## Task 29 (examples for new packages)
- `statstest.NewClient` needs a `testing.TB`, so `main` examples cannot use it; use `debugstats.Exporter{Dst, Grep}` as the in-process capture/console instead. Two debugstats exporters in one client collide on `Name()` ("duplicate exporter name"); wrap one in a struct that overrides `Name()`.
- `stats.Clock` / `otel.Clock` (clock.go, otel/clock.go, and tests) exist only as UNTRACKED files in the main checkout; they are on no branch and not on main. The clock example therefore demonstrates `Client.Observe` / `DurationObserver` with a manual "stamp" attribute; switch it to `client.Clock(name)` / `Stamp` / `Stop` once clock.go is committed. Note that the untracked clock.go still calls `Histogram(d.Seconds())`, not `Observe`, which the plan's task 7 asks to change.
- Prometheus pull: `Flush` before scraping, as metrics reach the handler asynchronously; counters render as `<name>_total`.
- httpstats client duration is recorded on response body close, so examples must drain and close the body before `Flush`. netstats flushes read/write totals on `Close`, so close both ends before `Flush`.
- `for line := range strings.SplitSeq(...)` is fine with Go 1.25.

## Task 30 (README, docs, CHANGELOG, CLAUDE.md)
- `feat/sp-38-docs` does NOT contain the delay metrics: `feat/sp-36c-delay-metrics` (199ffef, `WithRuntimeDelayMetrics`, `cpu.delay.seconds` and friends) is not merged, so `go doc` here shows only `runtimemetrics.Get`/`DelayInfo`/`IsUnsupported` and a no-op `Config.DelayMetrics`. The delay docs (`docs/runtime_metrics.md`, README migration row, CHANGELOG) are copied from the 36c branch's text; merge 36c before this branch, or drop those lines. Merging 36c later will conflict in CHANGELOG.md and docs/runtime_metrics.md (the same text, resolve by keeping one copy).
- The merged CHANGELOG had a stray `||||||| parent of f9ce7da` conflict marker line inside `[Unreleased]` (and the 36c branch carries another); removed here. Watch for it when merging 36c.
- The `v1.3.0` tag already contains the `service.instance.id` change, but the CHANGELOG still listed it under `[Unreleased]`. Cut it out as `[1.3.0] - 2026-10-06`; everything new is `[1.4.0] - Unreleased` (new minor). Added `[1.4.0]`/`[1.3.0]` compare links.
- `WithOTLP(&OTLPConfig{...})` states every transport field (endpoint, insecure, headers, timeout, compression, protocol, temporality) as an override, so zero values beat `OTEL_*`. To mix env and options use `WithOTLPFromEnv()` plus single-setting options. Documented in usage.md.
- `WithRuntimeProcessMetrics` godoc still says "collected on Linux"; Darwin is supported since #54. Docs say Linux and Darwin. Library godoc left untouched (no library edits allowed here).
- Datadog `Filters` default (`http_req_path`) is a visible behavior change for existing users, so CHANGELOG lists it under "Behavior changes".
- Never name a zsh variable `path` in a shell snippet: it is tied to `$PATH` and breaks every later command in a persistent shell. Use Python for link checks.
- Verification helpers (not committed): a `go doc -all` word-match script and a relative-link/anchor checker; evidence is in `.omo/evidence/segmentio-parity/30-happy.txt` and `30-fail.txt`. Remaining "missing" identifiers in the root and `models` are pre-existing (`MetricBuilder`, `RateLimiter`, ...), not added by this plan.
- F4 docs fix: README migration section now has a second table ("Adapted and not ported") with Mapped/Adapted/Not ported status for every segmentio v5.11.0 package and feature group (core Incr/Add/Set, tags, default engine, Measure/MakeMeasures, suffix buckets, Buffer, version, httpstats, netstats, procstats, otlp, debugstats, datadog String/Format, cmd/dogstatsd, grafana, util/objconv). Parity claims in README and CHANGELOG now say "ports the features listed in the migration tables".
- Verified against code: `STATS_DISABLE_GO_VERSION_REPORTING` accepts only true/TRUE/yes/1 (segmentio also accepts `on`); runtimemetrics has no NumCPU, Lookups or heap-sys metric; process cpu.usage.percent is one gauge (CPU delta over wall time and GOMAXPROCS); memory.total.bytes is MemTotal capped by cgroup v2 memory.max; netstats conn embeds net.Conn (no BaseConn, deadline errors not counted); OTLP resource has hostname-based service.instance.id but no host/process detection; duplicate attribute keys collapse via attribute.NewSet (last wins).
- Upstream: AllowDuplicateTags is opt-in, default dedupes, so our later-wins matches the default; only the opt-in is missing.

## Review fixes F1/F2 (debugstats import, lint, file size)
- `debugstats` may import only `stats`, `models`, `exporters`. `exporters.NewLineSerializer()` (exporters/lines.go) wraps the DogStatsD serializer, so debugstats no longer imports `serializers` directly; output unchanged. (`exporters` itself now imports `serializers`; no cycle, as `serializers` only imports `models`.)
- Pure-LOC check: strip blank and `//` lines (`grep -v '^\s*$' | grep -v '^\s*//' | wc -l`); the ceiling is 250. Splits are same-package, by responsibility; shared test helpers live in `*_helpers_test.go`. Test count (`go test -list . ./...`) was identical before and after (621).
- Example lint: `main` calls `run() error` (so `defer` runs before `os.Exit`); `net.Listen`/`DialTimeout` become `net.ListenConfig.Listen(ctx, ...)`/`net.Dialer.DialContext`, which needs the ctx created before the listener; `defer x.Close()` becomes `defer func() { _ = x.Close() }()`; gocyclo on `run()` fixed by extracting `record` and `scrape` helpers.
- A golangci-lint cache shared across worktrees prints "failed to get doc" warnings for files of deleted worktrees; harmless, but use a per-worktree `GOLANGCI_LINT_CACHE`.

## gap-netstats
- Upstream zone discovery (conn.go zoneOf/currentZone) uses github.com/segmentio/vpcinfo (AWS metadata subnets -> AZ names), not address inspection. Only an offline address-class equivalent is possible without a dependency; in_zone then means same network class, not same AZ.
- Upstream `BaseConn()` is just a method on the unexported conn. An exported interface named BaseConn cannot be embedded in a struct and still expose the method (field name shadows it), so users implement it rather than embed it.

## httpstats gap closure
- Tag keys must match `^[a-zA-Z_][a-zA-Z0-9_]*(\.…)*$`, so a `-` in a key (e.g. `http.request.header.content-type`) makes `validTagKey` reject the whole metric silently; use `content_type`.
- `NewHandler*`/`NewTransport*` gained variadic `...Option` (`WithContentAttributes`); source compatible, but not for code that stores them in a func-typed variable.
- Request/response counts deliberately not added: duration histogram count already is the request count.

## Runtime metrics gap closure (procstats)
- `MemStats.Lookups` is declared but never written by the Go runtime (only the heap dumper reads it) and has no `runtime/metrics` source, so it is documented as not provided instead of emitted as a constant zero.
- Go defines `MemStats.Alloc` as `HeapAlloc`; `memory.alloc` and `memory.heap.alloc` are the same value from the same sample. `HeapSys` = heap/objects + heap/unused + heap/free + heap/released.
- Pre-existing, not changed: `memory.heap.inuse` and `memory.heap.idle` map to `/memory/classes/heap/inuse:bytes` and `/idle:bytes`, which do not exist in `runtime/metrics` (Go 1.27 lists only heap/free, objects, released, stacks, unused), so those two names are never emitted. `TestExistingNamesUnchanged` hides it because it only asserts names whose source exists. Fix would be a derived mapping (inuse = objects+unused, idle = free+released) in a separate change.
- New CPU percent series use new names (`cpu.usage_user.percent`...) rather than a `type` attribute on `cpu.usage.percent`: a same-name typed series would double count when a backend sums the untyped one. They divide by the cgroup quota in cores when set, else GOMAXPROCS; `cpu.usage.percent` keeps GOMAXPROCS.
- `runtimemetrics.Collector` is already the built-in struct, so the upstream `Collector` interface is `MetricCollector` here.
- testify `Eventually` runs its condition on its own goroutine, so a goroutine-leak assertion inside it counts the helper; poll by hand.
- BSD `sed -i` needs `-i ''`; a failing `sed` in a `&&` chain silently skipped the test step and a commit still ran after `;`. Chain with `&&` all the way.
- Verified on Linux by cross-compiling test binaries (`GOOS=linux GOARCH=arm64 go test -c`) and running them in `debian:stable-slim` under docker with `--cpus 1.5`: real `/proc`, cgroup v2 `cpu.max` (150000 100000) and `CollectProcInfo` of a child process all work.
