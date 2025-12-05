# Integration Tests

This directory contains integration tests that validate the stats library against real backend services using Docker.

## Prerequisites

- Docker and Docker Compose installed
- Go 1.25+ installed
- Ports 8125, 9090, 9102, 9125, and 9999 available

## Backend Services

The integration tests use the following services:

1. **Datadog Agent** (port 8125) - DogStatsD server for testing Datadog metrics
2. **StatsD Exporter** (port 9125) - Receives StatsD metrics and exposes them for Prometheus
3. **Prometheus** (port 9090) - Scrapes metrics from StatsD exporter
4. **UDP Receiver** (port 9999) - Simple UDP receiver for testing

## Running Integration Tests

### Quick Start

```bash
# From repository root
make integration-test
```

This will:
1. Start all Docker services
2. Wait for services to be healthy
3. Run all integration tests
4. Stop and clean up services

### Manual Control

Start services manually:
```bash
make integration-up
```

Run tests while services are running:
```bash
go test -v -tags=integration ./test/integration/...
```

Stop services:
```bash
make integration-down
```

### View Logs

```bash
make integration-logs
```

Or for specific services:
```bash
cd test/integration
docker-compose logs -f datadog
docker-compose logs -f statsd-exporter
docker-compose logs -f prometheus
```

## Test Cases

### 1. Datadog Integration Test
- **Test**: `TestDatadogIntegration`
- **Purpose**: Validates metrics are sent to Datadog DogStatsD agent
- **Metrics Tested**: Counters, Gauges, Histograms
- **Volume**: 225 metrics total (100 counters, 50 gauges, 75 histograms)

### 2. Prometheus Integration Test
- **Test**: `TestPrometheusIntegration`
- **Purpose**: Validates metrics are sent via StatsD and queryable from Prometheus
- **Metrics Tested**: Counters and Gauges
- **Verification**: Queries Prometheus metrics endpoint to confirm delivery

### 3. Multi-Backend Integration Test
- **Test**: `TestMultiBackendIntegration`
- **Purpose**: Validates concurrent sending to multiple backends
- **Concurrency**: 10 goroutines sending metrics simultaneously
- **Volume**: 200 metrics across both backends

### 4. Rate Limiting Integration Test
- **Test**: `TestRateLimitingIntegration`
- **Purpose**: Validates rate limiting functionality
- **Rate Limit**: 100 metrics/sec with burst of 50
- **Test Scenario**: Attempts to send 500 metrics rapidly to trigger rate limiting

## Troubleshooting

### Services Won't Start

Check if ports are already in use:
```bash
lsof -i :8125
lsof -i :9090
lsof -i :9102
lsof -i :9125
```

### Metrics Not Appearing

1. Check service health:
```bash
cd test/integration
docker-compose ps
```

2. View service logs:
```bash
make integration-logs
```

3. Verify connectivity:
```bash
# Test Datadog UDP port
echo "test:1|c" | nc -u -w1 localhost 8125

# Test StatsD exporter
echo "test:1|c" | nc -u -w1 localhost 9125

# Check Prometheus metrics
curl http://localhost:9102/metrics
```

### Tests Failing

Run with verbose output:
```bash
make integration-test-verbose
```

Or run specific tests:
```bash
go test -v -tags=integration ./test/integration/... -run TestDatadogIntegration
```

## Configuration Files

- `docker-compose.yml` - Defines all backend services
- `statsd_mapping.yml` - StatsD exporter metric mapping rules
- `prometheus.yml` - Prometheus scrape configuration

## Expected Results

When all tests pass, you should see:

```
=== RUN   TestDatadogIntegration
    Datadog Stats: Processed=225, Dropped=0, Errors=0
--- PASS: TestDatadogIntegration

=== RUN   TestPrometheusIntegration
    Prometheus Stats: Processed=75, Dropped=0, Errors=0
--- PASS: TestPrometheusIntegration

=== RUN   TestMultiBackendIntegration
    Multi-Backend Stats: Processed=200, Dropped=0, Errors=0
--- PASS: TestMultiBackendIntegration

=== RUN   TestRateLimitingIntegration
    Rate Limiting Stats: Processed=150, RateLimited=350
    Rate Limiter: Rate=100/sec, Burst=50, Utilization=XX%
--- PASS: TestRateLimitingIntegration
```

## Cleanup

The integration test targets automatically clean up after themselves. To manually clean up:

```bash
make integration-down
```

To remove all Docker volumes and images:
```bash
cd test/integration
docker-compose down -v --rmi all
```

## CI/CD Integration

To run integration tests in CI/CD pipelines:

```yaml
# Example GitHub Actions
- name: Run Integration Tests
  run: |
    make integration-test
```

Note: Ensure Docker is available in your CI environment.
