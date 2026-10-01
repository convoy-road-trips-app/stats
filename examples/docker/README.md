# Docker example: OTLP/HTTP to Prometheus `_bucket` series

A small service built from this repository records a counter, a gauge and a latency histogram and exports them over OTLP/HTTP. An OpenTelemetry Collector receives them and serves them to Prometheus, which shows the histogram as `_bucket{le}` series.

```
demo (examples/docker/main.go) --OTLP/HTTP :4318--> collector --:8889--> Prometheus (:9090)
```

| Service | Image | Host port |
|---|---|---|
| `demo` | built from `Dockerfile` (Go 1.26.0, distroless runtime) | none |
| `collector` | `otel/opentelemetry-collector-contrib:0.115.1` | `18889` (Prometheus text format) |
| `prometheus` | `prom/prometheus:v3.0.0` | `19090` (query API) |

## Run

Needs Docker with the Compose plugin.

```bash
cd examples/docker
docker compose up --build -d
```

The demo exports every second. Wait about 15 seconds, then check.

## Verify the `_bucket` series

Prometheus, one series per bound (12 values of `le`: the 11 default bounds in seconds and `+Inf`):

```bash
curl -s http://127.0.0.1:19090/api/v1/query \
  --data-urlencode 'query=sum by (le) (demo_request_duration_seconds_bucket)'
```

Raw series, per route:

```bash
curl -s 'http://127.0.0.1:19090/api/v1/query?query=demo_request_duration_seconds_bucket'
```

The collector's own endpoint, without Prometheus:

```bash
curl -s http://127.0.0.1:18889/metrics | grep demo_request_duration_seconds_bucket
```

PromQL for a 95th percentile latency over the last 30 seconds:

```
histogram_quantile(0.95, sum by (le) (rate(demo_request_duration_seconds_bucket[30s])))
```

Other series: `demo_requests_total{route=...}` (counter) and `demo_queue_depth` (gauge). Prometheus UI: <http://127.0.0.1:19090>.

## Stop

```bash
docker compose down --volumes
```

`docker compose logs demo` shows the demo's errors, if any. On SIGTERM it drains its buffer before exiting.

## What to copy

`main.go` uses `stats.OTLPProtocolHTTP`, `Insecure: true` for the plain-HTTP collector, and `WithUDPTimeout(5*time.Second)`: background exports are bounded by that timeout (default 100 ms), which is too short for a collector behind a container network. Cumulative temporality (the default) is what Prometheus' OTLP ingestion and the collector's Prometheus exporter need. Configuration: [docs/usage.md](../../docs/usage.md).

## Tests

| Test | Command | Needs Docker |
|---|---|---|
| Demo exports the histogram, counter and gauge to a local OTLP/HTTP receiver | `go test ./examples/docker` | no |
| Whole stack serves all 12 `_bucket` series | `go test -count=1 -tags docker ./examples/docker` | yes |

The Docker test runs `docker compose up --build`, polls Prometheus until every bound is present (3 minute limit), prints the compose logs on failure and tears the stack down. It uses host ports 18889 and 19090.
