# Benchmarking Guide for Stats Library

## Overview

This guide provides comprehensive benchmarking strategies to validate performance characteristics of the non-blocking stats library.

---

## Benchmark Categories

### 1. Microbenchmarks (Component Level)

Test individual components in isolation.

#### Buffer Operations

```go
// benchmark/buffer_test.go
package benchmark

import (
    "sync/atomic"
    "testing"
)

// Ring Buffer Push Performance
func BenchmarkRingBufferPush(b *testing.B) {
    sizes := []int{1024, 4096, 8192, 16384}

    for _, size := range sizes {
        b.Run(fmt.Sprintf("size_%d", size), func(b *testing.B) {
            rb := NewRingBuffer(size)
            event := StatEvent{
                Type:  MetricType,
                Name:  "test.metric",
                Value: 123.45,
                Tags:  []Tag{{"env", "prod"}},
            }

            b.ResetTimer()
            b.ReportAllocs()

            for i := 0; i < b.N; i++ {
                if !rb.TryPush(event) {
                    b.Fatal("buffer full")
                }
            }

            ops := float64(b.N) / b.Elapsed().Seconds()
            b.ReportMetric(ops, "ops/sec")
            b.ReportMetric(float64(b.AllocedBytesPerOp()), "B/op")
        })
    }
}

// Ring Buffer Pop Performance
func BenchmarkRingBufferPop(b *testing.B) {
    rb := NewRingBuffer(16384)
    event := StatEvent{
        Type:  MetricType,
        Name:  "test.metric",
        Value: 123.45,
    }

    // Pre-fill buffer
    for i := 0; i < 10000; i++ {
        rb.TryPush(event)
    }

    b.ResetTimer()
    b.ReportAllocs()

    for i := 0; i < b.N; i++ {
        if _, ok := rb.TryPop(); !ok {
            // Refill
            for j := 0; j < 1000; j++ {
                rb.TryPush(event)
            }
        }
    }
}

// Concurrent Access (MPSC pattern)
func BenchmarkRingBufferConcurrent(b *testing.B) {
    rb := NewRingBuffer(16384)
    event := StatEvent{
        Type:  MetricType,
        Name:  "test.metric",
        Value: 123.45,
    }

    b.RunParallel(func(pb *testing.PB) {
        for pb.Next() {
            rb.TryPush(event)
        }
    })
}

// Channel Buffer Comparison
func BenchmarkChannelBuffer(b *testing.B) {
    cb := NewChannelBuffer(16384)
    event := StatEvent{
        Type:  MetricType,
        Name:  "test.metric",
        Value: 123.45,
    }

    b.ResetTimer()
    b.ReportAllocs()

    for i := 0; i < b.N; i++ {
        cb.TryPush(event, 0)
    }

    ops := float64(b.N) / b.Elapsed().Seconds()
    b.ReportMetric(ops, "ops/sec")
}
```

**Expected Results:**

```
BenchmarkRingBufferPush/size_8192-8          50000000    25.3 ns/op    0 B/op    0 allocs/op    39.5M ops/sec
BenchmarkRingBufferPop-8                     100000000   11.2 ns/op    0 B/op    0 allocs/op    89.3M ops/sec
BenchmarkRingBufferConcurrent-8              30000000    45.7 ns/op    0 B/op    0 allocs/op    21.9M ops/sec
BenchmarkChannelBuffer-8                     10000000    156 ns/op     0 B/op    0 allocs/op    6.4M ops/sec
```

---

#### Serialization Performance

```go
func BenchmarkSerialization(b *testing.B) {
    event := StatEvent{
        Type:      MetricType,
        Name:      "http.request.duration",
        Value:     123.45,
        Tags:      []Tag{{"env", "prod"}, {"host", "web01"}, {"method", "GET"}},
        Timestamp: time.Now().UnixNano(),
    }

    serializers := map[string]Serializer{
        "JSON":        NewJSONSerializer(),
        "MessagePack": NewMessagePackSerializer(),
        "Protobuf":    NewProtobufSerializer(),
        "Binary":      NewBinarySerializer(),
    }

    for name, serializer := range serializers {
        b.Run(name+"/encode", func(b *testing.B) {
            b.ReportAllocs()
            var result []byte
            for i := 0; i < b.N; i++ {
                result = serializer.Serialize(event)
            }
            b.SetBytes(int64(len(result)))
        })

        b.Run(name+"/decode", func(b *testing.B) {
            encoded := serializer.Serialize(event)
            b.ReportAllocs()
            b.ResetTimer()

            for i := 0; i < b.N; i++ {
                _ = serializer.Deserialize(encoded)
            }
        })

        // Size comparison
        encoded := serializer.Serialize(event)
        b.Logf("%s: %d bytes", name, len(encoded))
    }
}
```

**Expected Results:**

```
BenchmarkSerialization/JSON/encode-8        1000000    1247 ns/op    180 B      5 allocs/op
BenchmarkSerialization/JSON/decode-8         500000    2856 ns/op    512 B      12 allocs/op
BenchmarkSerialization/MessagePack/encode-8 2000000     478 ns/op     95 B      2 allocs/op
BenchmarkSerialization/MessagePack/decode-8 1500000     697 ns/op    128 B      4 allocs/op
BenchmarkSerialization/Protobuf/encode-8    3000000     293 ns/op     68 B      1 allocs/op
BenchmarkSerialization/Protobuf/decode-8    2500000     334 ns/op     96 B      2 allocs/op
BenchmarkSerialization/Binary/encode-8      8000000     127 ns/op     62 B      1 allocs/op
BenchmarkSerialization/Binary/decode-8      6000000     148 ns/op     64 B      1 allocs/op

Sizes:
JSON: 180 bytes
MessagePack: 95 bytes
Protobuf: 68 bytes
Binary: 62 bytes
```

---

#### Batching Performance

```go
func BenchmarkBatching(b *testing.B) {
    rb := NewRingBuffer(16384)
    batchSizes := []int{10, 20, 50, 100, 200}

    // Pre-fill buffer
    for i := 0; i < 10000; i++ {
        rb.TryPush(StatEvent{
            Type:  MetricType,
            Name:  "test.metric",
            Value: float64(i),
        })
    }

    for _, batchSize := range batchSizes {
        b.Run(fmt.Sprintf("size_%d", batchSize), func(b *testing.B) {
            processor := NewBatchProcessor(rb, batchSize, 100*time.Millisecond)
            batch := make([]StatEvent, 0, batchSize)

            b.ResetTimer()
            b.ReportAllocs()

            for i := 0; i < b.N; i++ {
                batch = batch[:0]
                for len(batch) < batchSize {
                    if event, ok := rb.TryPop(); ok {
                        batch = append(batch, event)
                    } else {
                        break
                    }
                }
            }

            b.ReportMetric(float64(b.N*batchSize)/b.Elapsed().Seconds(), "events/sec")
        })
    }
}
```

---

### 2. Integration Benchmarks (End-to-End)

Test complete pipeline from recording to sending.

```go
func BenchmarkEndToEnd(b *testing.B) {
    // Setup UDP listener to consume stats
    listener := startUDPListener(b, "localhost:0")
    defer listener.Close()

    config := &Config{
        BufferSize:  8192,
        BatchSize:   50,
        MaxBatchAge: 100 * time.Millisecond,
        UDPAddress:  listener.Addr().String(),
    }

    client := NewStatsClient(config)
    defer client.Close()

    b.ResetTimer()
    b.ReportAllocs()

    for i := 0; i < b.N; i++ {
        client.RecordMetric("benchmark.test", float64(i),
            Tag{"iteration", fmt.Sprintf("%d", i)},
        )
    }

    // Wait for all events to be sent
    client.Flush()

    received := listener.EventsReceived()
    sent := b.N

    b.ReportMetric(float64(sent)/b.Elapsed().Seconds(), "sent/sec")
    b.ReportMetric(float64(received)/float64(sent)*100, "delivery_%")
}

func BenchmarkEndToEndConcurrent(b *testing.B) {
    listener := startUDPListener(b, "localhost:0")
    defer listener.Close()

    config := &Config{
        BufferSize:  16384,
        BatchSize:   100,
        MaxBatchAge: 100 * time.Millisecond,
        UDPAddress:  listener.Addr().String(),
    }

    client := NewStatsClient(config)
    defer client.Close()

    b.ResetTimer()
    b.ReportAllocs()
    b.RunParallel(func(pb *testing.PB) {
        i := 0
        for pb.Next() {
            client.RecordMetric("benchmark.concurrent", float64(i))
            i++
        }
    })

    client.Flush()

    ops := float64(b.N) / b.Elapsed().Seconds()
    b.ReportMetric(ops, "ops/sec")
}
```

---

### 3. Stress Benchmarks

Push system to limits.

```go
func BenchmarkStress(b *testing.B) {
    scenarios := []struct {
        name        string
        goroutines  int
        bufferSize  int
        batchSize   int
    }{
        {"baseline", 1, 8192, 50},
        {"moderate", 4, 16384, 100},
        {"high", 8, 32768, 200},
        {"extreme", 16, 65536, 500},
    }

    for _, scenario := range scenarios {
        b.Run(scenario.name, func(b *testing.B) {
            listener := startUDPListener(b, "localhost:0")
            defer listener.Close()

            config := &Config{
                BufferSize:  scenario.bufferSize,
                BatchSize:   scenario.batchSize,
                MaxBatchAge: 50 * time.Millisecond,
                UDPAddress:  listener.Addr().String(),
            }

            client := NewStatsClient(config)
            defer client.Close()

            b.ResetTimer()
            b.SetParallelism(scenario.goroutines)
            b.RunParallel(func(pb *testing.PB) {
                for pb.Next() {
                    client.RecordMetric("stress.test", 1.0)
                }
            })

            client.Flush()

            metrics := client.ExportMetrics()
            throughput := metrics["throughput"].(map[string]uint64)

            b.ReportMetric(float64(throughput["events_sent"])/b.Elapsed().Seconds(), "sent/sec")
            b.ReportMetric(float64(throughput["events_dropped"])/float64(b.N)*100, "drop_%")
        })
    }
}
```

---

### 4. Memory Benchmarks

Track allocations and GC pressure.

```go
func BenchmarkMemoryPressure(b *testing.B) {
    config := &Config{
        BufferSize:      16384,
        BatchSize:       100,
        MaxBatchAge:     100 * time.Millisecond,
        UDPAddress:      "localhost:8125",
        EnablePooling:   true,
        EnableInterning: true,
    }

    client := NewStatsClient(config)
    defer client.Close()

    // Force GC before benchmark
    runtime.GC()

    var memBefore runtime.MemStats
    runtime.ReadMemStats(&memBefore)

    b.ResetTimer()
    b.ReportAllocs()

    for i := 0; i < b.N; i++ {
        client.RecordMetric("memory.test", float64(i))
    }

    client.Flush()

    var memAfter runtime.MemStats
    runtime.ReadMemStats(&memAfter)

    b.ReportMetric(float64(memAfter.TotalAlloc-memBefore.TotalAlloc)/float64(b.N), "B/op")
    b.ReportMetric(float64(memAfter.Mallocs-memBefore.Mallocs)/float64(b.N), "allocs/op")

    // GC stats
    gcPauses := memAfter.PauseNs[(memAfter.NumGC+255)%256]
    b.ReportMetric(float64(gcPauses)/1e6, "gc_pause_ms")
}

func BenchmarkPoolEfficiency(b *testing.B) {
    pool := &sync.Pool{
        New: func() interface{} {
            return &StatEvent{Tags: make([]Tag, 0, 8)}
        },
    }

    b.Run("with_pooling", func(b *testing.B) {
        b.ReportAllocs()
        for i := 0; i < b.N; i++ {
            event := pool.Get().(*StatEvent)
            event.Name = "test"
            event.Value = 123.45
            pool.Put(event)
        }
    })

    b.Run("without_pooling", func(b *testing.B) {
        b.ReportAllocs()
        for i := 0; i < b.N; i++ {
            event := &StatEvent{
                Name:  "test",
                Value: 123.45,
                Tags:  make([]Tag, 0, 8),
            }
            _ = event
        }
    })
}
```

---

### 5. Latency Benchmarks

Measure end-to-end latency distribution.

```go
func BenchmarkLatency(b *testing.B) {
    listener := startUDPListener(b, "localhost:0")
    defer listener.Close()

    config := &Config{
        BufferSize:  16384,
        BatchSize:   50,
        MaxBatchAge: 100 * time.Millisecond,
        UDPAddress:  listener.Addr().String(),
    }

    client := NewStatsClient(config)
    defer client.Close()

    latencies := make([]time.Duration, b.N)

    b.ResetTimer()

    for i := 0; i < b.N; i++ {
        start := time.Now()
        client.RecordMetric("latency.test", float64(i))
        latencies[i] = time.Since(start)
    }

    client.Flush()

    // Calculate percentiles
    sort.Slice(latencies, func(i, j int) bool {
        return latencies[i] < latencies[j]
    })

    p50 := latencies[len(latencies)*50/100]
    p95 := latencies[len(latencies)*95/100]
    p99 := latencies[len(latencies)*99/100]
    p999 := latencies[len(latencies)*999/1000]

    b.ReportMetric(float64(p50.Nanoseconds()), "p50_ns")
    b.ReportMetric(float64(p95.Nanoseconds()), "p95_ns")
    b.ReportMetric(float64(p99.Nanoseconds()), "p99_ns")
    b.ReportMetric(float64(p999.Nanoseconds()), "p999_ns")
}
```

---

## Load Testing Framework

### Complete Load Test Implementation

```go
// cmd/loadtest/main.go
package main

import (
    "context"
    "flag"
    "fmt"
    "os"
    "os/signal"
    "runtime"
    "sync"
    "sync/atomic"
    "syscall"
    "time"
)

type LoadTestConfig struct {
    TargetRate  int           // Events per second
    Duration    time.Duration // Test duration
    Concurrency int           // Number of worker goroutines
    BufferSize  int           // Client buffer size
    BatchSize   int           // Batch size
    MaxBatchAge time.Duration // Max batch age
    UDPAddress  string        // Stats destination
}

type LoadTestResults struct {
    EventsGenerated uint64
    EventsSent      uint64
    EventsDropped   uint64
    EventsReceived  uint64

    LatencyP50  time.Duration
    LatencyP95  time.Duration
    LatencyP99  time.Duration
    LatencyP999 time.Duration

    BufferUtilPeak float64
    MemoryPeakMB   float64

    StartTime time.Time
    EndTime   time.Time
}

type LoadTest struct {
    config  LoadTestConfig
    client  *StatsClient
    results LoadTestResults

    eventsGenerated uint64
    eventsDropped   uint64

    latencies []time.Duration
    mu        sync.Mutex
}

func main() {
    config := parseFlags()

    fmt.Printf("=== Stats Library Load Test ===\n")
    fmt.Printf("Target Rate: %d events/sec\n", config.TargetRate)
    fmt.Printf("Duration: %s\n", config.Duration)
    fmt.Printf("Concurrency: %d workers\n", config.Concurrency)
    fmt.Printf("Buffer Size: %d\n", config.BufferSize)
    fmt.Printf("Batch Size: %d\n", config.BatchSize)
    fmt.Printf("UDP Address: %s\n", config.UDPAddress)
    fmt.Printf("\n")

    lt := NewLoadTest(config)
    defer lt.client.Close()

    // Handle graceful shutdown
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

    go func() {
        <-sigCh
        fmt.Println("\nShutdown requested...")
        cancel()
    }()

    // Run test
    lt.Run(ctx)

    // Print results
    lt.PrintResults()
}

func parseFlags() LoadTestConfig {
    config := LoadTestConfig{}

    flag.IntVar(&config.TargetRate, "rate", 10000, "Target events per second")
    flag.DurationVar(&config.Duration, "duration", 30*time.Second, "Test duration")
    flag.IntVar(&config.Concurrency, "concurrency", runtime.NumCPU(), "Number of workers")
    flag.IntVar(&config.BufferSize, "buffer", 16384, "Client buffer size")
    flag.IntVar(&config.BatchSize, "batch", 50, "Batch size")
    flag.DurationVar(&config.MaxBatchAge, "batch-age", 100*time.Millisecond, "Max batch age")
    flag.StringVar(&config.UDPAddress, "addr", "localhost:8125", "UDP destination")

    flag.Parse()

    return config
}

func NewLoadTest(config LoadTestConfig) *LoadTest {
    clientConfig := &Config{
        BufferSize:  config.BufferSize,
        BatchSize:   config.BatchSize,
        MaxBatchAge: config.MaxBatchAge,
        UDPAddress:  config.UDPAddress,
    }

    return &LoadTest{
        config:    config,
        client:    NewStatsClient(clientConfig),
        latencies: make([]time.Duration, 0, 100000),
    }
}

func (lt *LoadTest) Run(ctx context.Context) {
    testCtx, cancel := context.WithTimeout(ctx, lt.config.Duration)
    defer cancel()

    lt.results.StartTime = time.Now()

    var wg sync.WaitGroup

    // Start monitoring
    wg.Add(1)
    go lt.monitor(testCtx, &wg)

    // Start workers
    eventsPerWorker := lt.config.TargetRate / lt.config.Concurrency
    if eventsPerWorker == 0 {
        eventsPerWorker = 1
    }

    for i := 0; i < lt.config.Concurrency; i++ {
        wg.Add(1)
        go lt.worker(testCtx, &wg, eventsPerWorker, i)
    }

    wg.Wait()

    lt.results.EndTime = time.Now()

    // Final flush
    lt.client.Flush()

    // Collect final metrics
    lt.collectFinalMetrics()
}

func (lt *LoadTest) worker(ctx context.Context, wg *sync.WaitGroup, targetRate, id int) {
    defer wg.Done()

    if targetRate == 0 {
        return
    }

    interval := time.Second / time.Duration(targetRate)
    ticker := time.NewTicker(interval)
    defer ticker.Stop()

    metricName := fmt.Sprintf("loadtest.worker.%d", id)

    for {
        select {
        case <-ctx.Done():
            return

        case <-ticker.C:
            start := time.Now()

            atomic.AddUint64(&lt.eventsGenerated, 1)

            success := lt.client.RecordMetric(metricName, float64(time.Now().UnixNano()))

            if !success {
                atomic.AddUint64(&lt.eventsDropped, 1)
            }

            // Sample latency (1% of requests)
            if lt.eventsGenerated%100 == 0 {
                latency := time.Since(start)
                lt.mu.Lock()
                lt.latencies = append(lt.latencies, latency)
                lt.mu.Unlock()
            }
        }
    }
}

func (lt *LoadTest) monitor(ctx context.Context, wg *sync.WaitGroup) {
    defer wg.Done()

    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()

    lastGenerated := uint64(0)
    lastSent := uint64(0)

    fmt.Printf("%-8s %12s %12s %12s %10s %10s %8s\n",
        "Time", "Generated", "Sent", "Dropped", "Rate", "Drop%", "Buffer%")
    fmt.Println(strings.Repeat("-", 80))

    for {
        select {
        case <-ctx.Done():
            return

        case <-ticker.C:
            generated := atomic.LoadUint64(&lt.eventsGenerated)
            dropped := atomic.LoadUint64(&lt.eventsDropped)

            metrics := lt.client.ExportMetrics()
            throughput := metrics["throughput"].(map[string]uint64)
            buffer := metrics["buffer"].(map[string]interface{})

            sent := throughput["events_sent"]
            bufferUtil := buffer["utilization"].(float64)

            // Track peak buffer utilization
            if bufferUtil > lt.results.BufferUtilPeak {
                lt.results.BufferUtilPeak = bufferUtil
            }

            // Track memory
            var memStats runtime.MemStats
            runtime.ReadMemStats(&memStats)
            memMB := float64(memStats.Alloc) / 1024 / 1024
            if memMB > lt.results.MemoryPeakMB {
                lt.results.MemoryPeakMB = memMB
            }

            // Calculate rates
            genRate := generated - lastGenerated
            sentRate := sent - lastSent
            dropRate := float64(dropped) / float64(generated) * 100

            fmt.Printf("%-8s %12d %12d %12d %10d %9.2f%% %7.1f%%\n",
                time.Now().Format("15:04:05"),
                generated,
                sent,
                dropped,
                genRate,
                dropRate,
                bufferUtil*100,
            )

            lastGenerated = generated
            lastSent = sent
        }
    }
}

func (lt *LoadTest) collectFinalMetrics() {
    lt.results.EventsGenerated = atomic.LoadUint64(&lt.eventsGenerated)
    lt.results.EventsDropped = atomic.LoadUint64(&lt.eventsDropped)

    metrics := lt.client.ExportMetrics()
    throughput := metrics["throughput"].(map[string]uint64)
    lt.results.EventsSent = throughput["events_sent"]

    // Calculate latency percentiles
    lt.mu.Lock()
    sort.Slice(lt.latencies, func(i, j int) bool {
        return lt.latencies[i] < lt.latencies[j]
    })

    if len(lt.latencies) > 0 {
        lt.results.LatencyP50 = lt.latencies[len(lt.latencies)*50/100]
        lt.results.LatencyP95 = lt.latencies[len(lt.latencies)*95/100]
        lt.results.LatencyP99 = lt.latencies[len(lt.latencies)*99/100]
        lt.results.LatencyP999 = lt.latencies[len(lt.latencies)*999/1000]
    }
    lt.mu.Unlock()
}

func (lt *LoadTest) PrintResults() {
    r := lt.results

    fmt.Printf("\n=== Load Test Results ===\n\n")

    duration := r.EndTime.Sub(r.StartTime)
    fmt.Printf("Duration: %s\n", duration.Round(time.Millisecond))

    fmt.Printf("\nThroughput:\n")
    fmt.Printf("  Target Rate:      %d events/sec\n", lt.config.TargetRate)
    fmt.Printf("  Events Generated: %d\n", r.EventsGenerated)
    fmt.Printf("  Events Sent:      %d\n", r.EventsSent)
    fmt.Printf("  Events Dropped:   %d\n", r.EventsDropped)
    fmt.Printf("  Effective Rate:   %.0f events/sec\n", float64(r.EventsGenerated)/duration.Seconds())
    fmt.Printf("  Drop Rate:        %.2f%%\n", float64(r.EventsDropped)/float64(r.EventsGenerated)*100)

    fmt.Printf("\nLatency:\n")
    fmt.Printf("  P50:  %s\n", r.LatencyP50)
    fmt.Printf("  P95:  %s\n", r.LatencyP95)
    fmt.Printf("  P99:  %s\n", r.LatencyP99)
    fmt.Printf("  P999: %s\n", r.LatencyP999)

    fmt.Printf("\nResource Usage:\n")
    fmt.Printf("  Peak Buffer Util: %.1f%%\n", r.BufferUtilPeak*100)
    fmt.Printf("  Peak Memory:      %.1f MB\n", r.MemoryPeakMB)

    // Pass/Fail criteria
    fmt.Printf("\nQuality Checks:\n")

    dropRate := float64(r.EventsDropped) / float64(r.EventsGenerated) * 100
    if dropRate < 1.0 {
        fmt.Printf("  ✓ Drop rate < 1%%: %.2f%%\n", dropRate)
    } else {
        fmt.Printf("  ✗ Drop rate >= 1%%: %.2f%%\n", dropRate)
    }

    if r.LatencyP99 < 100*time.Millisecond {
        fmt.Printf("  ✓ P99 latency < 100ms: %s\n", r.LatencyP99)
    } else {
        fmt.Printf("  ✗ P99 latency >= 100ms: %s\n", r.LatencyP99)
    }

    if r.MemoryPeakMB < 10.0 {
        fmt.Printf("  ✓ Peak memory < 10MB: %.1fMB\n", r.MemoryPeakMB)
    } else {
        fmt.Printf("  ✗ Peak memory >= 10MB: %.1fMB\n", r.MemoryPeakMB)
    }
}
```

---

## Running Benchmarks

### Quick Start

```bash
# Run all benchmarks
go test -bench=. -benchmem -benchtime=10s ./...

# Run specific benchmark
go test -bench=BenchmarkRingBuffer -benchmem ./benchmark

# With CPU profiling
go test -bench=BenchmarkEndToEnd -benchmem -cpuprofile=cpu.prof
go tool pprof cpu.prof

# With memory profiling
go test -bench=BenchmarkMemory -benchmem -memprofile=mem.prof
go tool pprof mem.prof

# Compare results
go test -bench=. -benchmem -count=5 | tee new.txt
benchstat old.txt new.txt
```

### Load Testing

```bash
# Baseline test
go run cmd/loadtest/main.go -rate=10000 -duration=30s

# High load test
go run cmd/loadtest/main.go -rate=100000 -duration=60s -concurrency=8

# Stress test
go run cmd/loadtest/main.go -rate=500000 -duration=30s -concurrency=16

# With custom configuration
go run cmd/loadtest/main.go \
    -rate=50000 \
    -duration=120s \
    -concurrency=4 \
    -buffer=32768 \
    -batch=100 \
    -batch-age=50ms \
    -addr=localhost:8125
```

---

## Continuous Performance Testing

### CI/CD Integration

```yaml
# .github/workflows/performance.yml
name: Performance Tests

on:
  pull_request:
  push:
    branches: [main]

jobs:
  benchmark:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
        with:
          go-version: '1.21'

      - name: Run benchmarks
        run: |
          go test -bench=. -benchmem -benchtime=10s ./... | tee benchmark.txt

      - name: Compare with baseline
        uses: benchmark-action/github-action-benchmark@v1
        with:
          tool: 'go'
          output-file-path: benchmark.txt
          alert-threshold: '150%'
          fail-on-alert: true

      - name: Load test
        run: |
          go run cmd/loadtest/main.go -rate=50000 -duration=30s > loadtest.txt

      - name: Check performance criteria
        run: |
          # Extract drop rate and fail if >1%
          DROP_RATE=$(grep "Drop Rate" loadtest.txt | awk '{print $3}' | tr -d '%')
          if (( $(echo "$DROP_RATE > 1.0" | bc -l) )); then
            echo "FAIL: Drop rate $DROP_RATE% exceeds 1%"
            exit 1
          fi
```

---

## Performance Regression Detection

```bash
# Baseline capture (before changes)
go test -bench=. -benchmem -count=10 ./... > baseline.txt

# After changes
go test -bench=. -benchmem -count=10 ./... > current.txt

# Compare
benchstat baseline.txt current.txt

# Example output:
# name                      old time/op    new time/op    delta
# RingBufferPush-8            25.3ns ± 2%    23.1ns ± 1%   -8.70%  (p=0.000 n=10+10)
# ChannelBuffer-8              156ns ± 1%     154ns ± 2%     ~     (p=0.052 n=10+10)
#
# name                      old alloc/op   new alloc/op   delta
# RingBufferPush-8            0.00B          0.00B          ~     (all equal)
```

---

## Performance Targets Summary

| Metric | Target | Method |
|--------|--------|--------|
| Push latency (p99) | <100ns | BenchmarkRingBufferPush |
| E2E latency (p99) | <100ms | BenchmarkLatency |
| Throughput | >100k/sec | BenchmarkEndToEnd |
| Drop rate | <1% | LoadTest |
| Memory (10k events) | <1MB | BenchmarkMemoryPressure |
| GC pause (p99) | <10ms | BenchmarkMemoryPressure |
| Allocations | 0 alloc/op | All benchmarks |

---

*Benchmarking Guide v1.0*
*Comprehensive performance validation for production readiness*
