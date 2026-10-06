# Learnings — segmentio-parity

Conventions, patterns, and successful approaches discovered during work on this plan.

---

## PR27 (test: shared per-metric buckets across exporters)
- A root-package test can exercise one real OTLP/HTTP receiver and a Prometheus pull Handler via one client, asserting bounds from both exported surfaces for named, global fallback, and default fallback cases.
- `attribute.NewSet` sorts its variadic attribute slice in place. Exporters run concurrently on shared metrics, so clone metric attributes before creating an OTel set in both the OTLP and Prometheus exporters to prevent a data race and mutation of pipeline-owned metrics.
- Verification: the focused shared-bucket test, requested lint/vet/race checks, module diff, and darwin/linux/windows builds passed; see `.omo/evidence/segmentio-parity/pr27.txt`.
- The task test uses the real client, OTLP/HTTP protobuf receiver, and Prometheus scrape endpoint; named, global fallback, and default fallback bounds are asserted on both surfaces.
