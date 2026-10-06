# Learnings — segmentio-parity

Conventions, patterns, and successful approaches discovered during work on this plan.

---

## PR27 (test: shared per-metric buckets across exporters)
- A root-package test can exercise one real OTLP/HTTP receiver and a Prometheus pull Handler via one client, asserting bounds from both exported surfaces for named, global fallback, and default fallback cases.
- `attribute.NewSet` sorts its variadic attribute slice in place. Exporters run concurrently on shared metrics, so clone metric attributes before creating an OTel set in both the OTLP and Prometheus exporters to prevent a data race and mutation of pipeline-owned metrics.
- Verification: the focused shared-bucket test, requested lint/vet/race checks, module diff, and darwin/linux/windows builds passed; see `.omo/evidence/segmentio-parity/pr27.txt`.
- The task test uses the real client, OTLP/HTTP protobuf receiver, and Prometheus scrape endpoint; named, global fallback, and default fallback bounds are asserted on both surfaces.
<<<<<<< HEAD

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
||||||| e3cbeb7
=======

## Task 29 (examples for new packages)
- `statstest.NewClient` needs a `testing.TB`, so `main` examples cannot use it; use `debugstats.Exporter{Dst, Grep}` as the in-process capture/console instead. Two debugstats exporters in one client collide on `Name()` ("duplicate exporter name"); wrap one in a struct that overrides `Name()`.
- `stats.Clock` / `otel.Clock` (clock.go, otel/clock.go, and tests) exist only as UNTRACKED files in the main checkout; they are on no branch and not on main. The clock example therefore demonstrates `Client.Observe` / `DurationObserver` with a manual "stamp" attribute; switch it to `client.Clock(name)` / `Stamp` / `Stop` once clock.go is committed. Note that the untracked clock.go still calls `Histogram(d.Seconds())`, not `Observe`, which the plan's task 7 asks to change.
- Prometheus pull: `Flush` before scraping, as metrics reach the handler asynchronously; counters render as `<name>_total`.
- httpstats client duration is recorded on response body close, so examples must drain and close the body before `Flush`. netstats flushes read/write totals on `Close`, so close both ends before `Flush`.
- `for line := range strings.SplitSeq(...)` is fine with Go 1.25.
>>>>>>> feat/sp-37-examples
