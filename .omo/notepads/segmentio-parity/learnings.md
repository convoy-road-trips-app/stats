# Learnings — segmentio-parity

Conventions, patterns, and successful approaches discovered during work on this plan.

---

## PR27 (test: shared per-metric buckets across exporters)
- A root-package test can exercise one real OTLP/HTTP receiver and a Prometheus pull Handler via one client, asserting bounds from both exported surfaces for named, global fallback, and default fallback cases.
- `attribute.NewSet` sorts its variadic attribute slice in place. Exporters run concurrently on shared metrics, so clone metric attributes before creating an OTel set in both the OTLP and Prometheus exporters to prevent a data race and mutation of pipeline-owned metrics.
- Verification: the focused shared-bucket test, requested lint/vet/race checks, module diff, and darwin/linux/windows builds passed; see `.omo/evidence/segmentio-parity/pr27.txt`.
- The task test uses the real client, OTLP/HTTP protobuf receiver, and Prometheus scrape endpoint; named, global fallback, and default fallback bounds are asserted on both surfaces.

## Task 29 (examples for new packages)
- `statstest.NewClient` needs a `testing.TB`, so `main` examples cannot use it; use `debugstats.Exporter{Dst, Grep}` as the in-process capture/console instead. Two debugstats exporters in one client collide on `Name()` ("duplicate exporter name"); wrap one in a struct that overrides `Name()`.
- `stats.Clock` / `otel.Clock` (clock.go, otel/clock.go, and tests) exist only as UNTRACKED files in the main checkout; they are on no branch and not on main. The clock example therefore demonstrates `Client.Observe` / `DurationObserver` with a manual "stamp" attribute; switch it to `client.Clock(name)` / `Stamp` / `Stop` once clock.go is committed. Note that the untracked clock.go still calls `Histogram(d.Seconds())`, not `Observe`, which the plan's task 7 asks to change.
- Prometheus pull: `Flush` before scraping, as metrics reach the handler asynchronously; counters render as `<name>_total`.
- httpstats client duration is recorded on response body close, so examples must drain and close the body before `Flush`. netstats flushes read/write totals on `Close`, so close both ends before `Flush`.
- `for line := range strings.SplitSeq(...)` is fine with Go 1.25.
