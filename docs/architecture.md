# High-Performance Stats Library Architecture

## System Overview

```
┌─────────────────────────────────────────────────────────────────────┐
│                        Application Code                              │
│                                                                       │
│  client.RecordMetric("http.requests", 1.0, Tag{"method", "GET"})   │
└───────────────────────────────┬─────────────────────────────────────┘
                                │
                                │ Non-blocking (<100ns)
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│                       Stats Client API Layer                         │
│                                                                       │
│  • Validate input                                                    │
│  • Apply drop strategy                                               │
│  • Pool object acquisition                                           │
│  • String interning                                                  │
└───────────────────────────────┬─────────────────────────────────────┘
                                │
                                │ Try Push (atomic)
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│                    Lock-Free Ring Buffer                             │
│                                                                       │
│  ┌──────┬──────┬──────┬──────┬──────┬──────┬──────┬──────┐         │
│  │Event │Event │Event │      │      │      │      │      │         │
│  │  0   │  1   │  2   │ ...  │      │      │      │ 8191 │         │
│  └──────┴──────┴──────┴──────┴──────┴──────┴──────┴──────┘         │
│     ▲                                                  ▲              │
│     │                                                  │              │
│   Head (write)                                      Tail (read)      │
│                                                                       │
│  Size: 8K-16K events, ~1MB memory                                   │
└───────────────────────────────┬─────────────────────────────────────┘
                                │
                                │ Pop batch (non-blocking)
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│                      Batch Processor                                 │
│                                                                       │
│  Triggers:                                                           │
│  • Size threshold (50 events)                                        │
│  • Time threshold (100ms)                                            │
│  • Buffer pressure (adaptive)                                        │
│                                                                       │
│  Batch Buffer (pre-allocated, reused):                              │
│  [Event1, Event2, Event3, ..., Event50]                             │
└───────────────────────────────┬─────────────────────────────────────┘
                                │
                                │ Serialize batch
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│                    Binary Serializer                                 │
│                                                                       │
│  Format: [Type:1][NameLen:2][Name:N][Value:8][TS:8][Tags...]       │
│                                                                       │
│  Performance: <200ns per event, 60-80 bytes per event               │
│  Reuses byte buffer from pool                                        │
└───────────────────────────────┬─────────────────────────────────────┘
                                │
                                │ Pack into UDP packets
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│                       UDP Packet Packer                              │
│                                                                       │
│  Packet Structure (1472 bytes max):                                 │
│  ┌──────────────────────────────────────────────────────┐           │
│  │ Header (16 bytes)                                     │           │
│  │  - Sequence: 8 bytes                                  │           │
│  │  - Timestamp: 8 bytes                                 │           │
│  ├──────────────────────────────────────────────────────┤           │
│  │ Event 1 (60-80 bytes)                                 │           │
│  │ Event 2 (60-80 bytes)                                 │           │
│  │ Event 3 (60-80 bytes)                                 │           │
│  │ ...                                                    │           │
│  │ Event ~20 (60-80 bytes)                               │           │
│  ├──────────────────────────────────────────────────────┤           │
│  │ Checksum (4 bytes)                                    │           │
│  └──────────────────────────────────────────────────────┘           │
│                                                                       │
│  Packs 15-25 events per packet                                      │
└───────────────────────────────┬─────────────────────────────────────┘
                                │
                                │ Send (non-blocking)
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│                         UDP Sender                                   │
│                                                                       │
│  • Circuit breaker protection                                        │
│  • Error counting                                                    │
│  • Write to socket (non-blocking)                                    │
│  • Accept packet loss (<2%)                                          │
│                                                                       │
│  ┌─────────────────────────────────────┐                            │
│  │        Network (UDP/IP)              │                            │
│  │                                       │                            │
│  │  Destination: StatsD/Telegraf/etc   │                            │
│  └─────────────────────────────────────┘                            │
└─────────────────────────────────────────────────────────────────────┘


                     ┌──────────────────────────┐
                     │   Monitoring Pipeline    │
                     │                          │
                     │  Internal Metrics:       │
                     │  • Events received       │
                     │  • Events sent           │
                     │  • Events dropped        │
                     │  • Buffer utilization    │
                     │  • Latency histogram     │
                     │  • UDP errors            │
                     │                          │
                     │  Health Check:           │
                     │  GET /health             │
                     └──────────────────────────┘
```

---

## Data Flow

### Happy Path (Normal Operation)

```
Time: 0ms
┌────────────┐
│Application │ RecordMetric("http.requests", 1.0)
└─────┬──────┘
      │ <100ns (acquire from pool, intern strings)
      ▼
┌────────────┐
│Ring Buffer │ TryPush → Success (buffer 30% full)
└─────┬──────┘
      │ 0ns (async, returns immediately)
      ▼
┌────────────┐
│Application │ Continues execution
└────────────┘


Time: 0-100ms (in background)
┌────────────┐
│Ring Buffer │ Accumulating events...
└─────┬──────┘
      │
      │ 50 events reached OR 100ms elapsed
      ▼
┌────────────┐
│Batch Proc  │ Pop 50 events
└─────┬──────┘
      │ 10μs (50 × 200ns serialization)
      ▼
┌────────────┐
│Serializer  │ Binary encode → 3KB payload
└─────┬──────┘
      │ 2μs (pack into 2 UDP packets)
      ▼
┌────────────┐
│UDP Packer  │ 2 packets of 1472 bytes each
└─────┬──────┘
      │ ~100μs (network latency)
      ▼
┌────────────┐
│UDP Sender  │ Write to network
└─────┬──────┘
      │
      ▼
┌────────────┐
│Stats       │ Receive and process
│Backend     │
└────────────┘

Total E2E Latency: 0-100ms (batching) + 0.2ms (processing)
```

---

### Backpressure Path (High Load)

```
┌────────────┐
│Application │ RecordMetric (high frequency)
└─────┬──────┘
      │
      ▼
┌────────────┐
│Ring Buffer │ Buffer 90% full → Adaptive batching activated
└─────┬──────┘
      │
      ├──────► Batch size increases to 100 events
      │        Flush frequency increases to 50ms
      │
      ▼
┌────────────┐
│Batch Proc  │ Faster drain rate
└────────────┘

If buffer reaches 100% full:
┌────────────┐
│Application │ RecordMetric
└─────┬──────┘
      │
      ▼
┌────────────┐
│Drop        │ Apply strategy:
│Strategy    │ • Priority → Drop low priority
│            │ • Sampling → Drop 50% randomly
│            │ • Oldest → Overwrite oldest event
└─────┬──────┘
      │
      ▼
┌────────────┐
│Metrics     │ Increment dropped counter
└────────────┘
```

---

### Circuit Breaker Path (Network Failure)

```
┌────────────┐
│UDP Sender  │ Send fails (network down)
└─────┬──────┘
      │
      │ Consecutive failures: 1, 2, 3, ... 10
      ▼
┌────────────┐
│Circuit     │ Failure threshold reached → OPEN
│Breaker     │
└─────┬──────┘
      │
      │ Stop sending, drop events
      ▼
┌────────────┐
│Drop        │ Increment dropped counter
│Strategy    │ Log warning (sampled)
└─────┬──────┘
      │
      │ Wait 30 seconds...
      ▼
┌────────────┐
│Circuit     │ Try half-open state
│Breaker     │ Test with single request
└─────┬──────┘
      │
      ├──► Success → CLOSED (resume normal operation)
      │
      └──► Failure → OPEN (wait another 30s)
```

---

## Component Details

### Ring Buffer

**Purpose:** Lock-free, bounded FIFO queue for events

**Key Features:**
- Power-of-2 sizing for fast modulo (bitwise AND)
- Cache line padding to prevent false sharing
- Atomic head/tail pointers
- Non-blocking push/pop operations

**Performance:**
- Push: 20-30ns
- Pop: 10-15ns
- Zero allocations
- 10-50M ops/sec throughput

**Memory:**
- 8K events × 64 bytes = 512KB
- 16K events × 64 bytes = 1MB (recommended)

---

### Batch Processor

**Purpose:** Aggregate events to reduce network overhead

**Key Features:**
- Time-based flush (100ms default)
- Size-based flush (50 events default)
- Adaptive sizing based on buffer pressure
- Pre-allocated batch buffer

**Trade-offs:**
- Smaller batches = lower latency, more network packets
- Larger batches = higher latency, fewer packets

**Recommended:**
- Batch size: 50 events
- Max age: 100ms
- Adaptive range: 10-100 events

---

### Binary Serializer

**Purpose:** Efficient encoding for network transmission

**Format:**
```
Event Layout (62-120 bytes typical):
┌────────┬──────────┬──────────┬─────────┬───────────┬──────────┐
│ Type   │ NameLen  │   Name   │  Value  │ Timestamp │   Tags   │
│ 1 byte │ 2 bytes  │ N bytes  │ 8 bytes │  8 bytes  │ Variable │
└────────┴──────────┴──────────┴─────────┴───────────┴──────────┘

Tag Layout (per tag):
┌──────────┬──────────┬──────────┬──────────┐
│  KeyLen  │   Key    │  ValLen  │  Value   │
│ 2 bytes  │ N bytes  │ 2 bytes  │ N bytes  │
└──────────┴──────────┴──────────┴──────────┘
```

**Performance:**
- Encoding: 100-200ns per event
- Size: 60-120 bytes per event (vs 180 bytes JSON)
- 3x smaller, 10x faster than JSON

---

### UDP Packet Packer

**Purpose:** Maximize network efficiency

**Strategy:**
- Target 1472 bytes per packet (safe MTU)
- Pack 15-25 events per packet
- Add sequence numbers for loss detection
- Add checksums for corruption detection

**Efficiency:**
```
Without packing:
100 events × 88 bytes (60 payload + 28 headers) = 8.8KB

With packing:
100 events ÷ 20 per packet = 5 packets
5 × 1472 bytes = 7.36KB

Savings: 16% bandwidth, 95% fewer packets
```

---

### Object Pools

**Purpose:** Reduce allocations and GC pressure

**Pooled Objects:**
1. **StatEvent**: Main event structure
2. **Tag Slices**: Reusable tag arrays
3. **Byte Buffers**: Serialization buffers

**Pool Configuration:**
```go
eventPool: StatEvent with Tags cap=8
tagPool: []Tag with cap=16
bufferPool: []byte with cap=2048
```

**Impact:**
- Allocation reduction: 80-95%
- GC pause reduction: 60-80%
- Critical for >10k events/sec

---

### Drop Strategies

**Purpose:** Handle backpressure gracefully

**Strategies:**

1. **DropNewest** (Default)
   - Reject incoming events
   - Preserve historical data
   - Best for: Analysis workloads

2. **DropOldest**
   - Overwrite oldest events
   - Maintain fresh data
   - Best for: Real-time monitoring

3. **DropByPriority**
   - Drop low priority first
   - Protect critical metrics
   - Best for: Mixed workloads

4. **DropSampled**
   - Statistical sampling
   - Maintain distribution
   - Best for: High cardinality

**Selection Guide:**
```
Use Case              | Strategy         | Rationale
----------------------|------------------|---------------------------
Real-time dashboards  | DropOldest      | Fresh data more valuable
Historical analysis   | DropNewest      | Complete dataset important
Production monitoring | DropByPriority  | Errors must get through
High cardinality      | DropSampled     | Statistical accuracy OK
```

---

## Performance Characteristics

### Throughput

```
Single Producer (1 CPU core):
  Ring Buffer: 10-50M events/sec
  End-to-End:  100k-500k events/sec (limited by batching/network)

Multiple Producers (8 CPU cores):
  Sharded Buffer: 50-200M events/sec
  End-to-End:     500k-2M events/sec
```

### Latency

```
Recording Latency (p99):
  Pooled:       <100ns
  Non-pooled:   <500ns

End-to-End Latency (p99):
  Low load:     10-50ms   (batch accumulation)
  Medium load:  50-100ms  (batch accumulation + processing)
  High load:    100-200ms (backpressure effects)
```

### Memory

```
Fixed Overhead:
  Ring Buffer:  1MB (16K events)
  String Cache: 50-500KB (1-10K unique strings)
  Batch Buffer: 32KB (pre-allocated)
  Pools:        128KB (pool overhead)
  Total:        ~2MB

Per-Event Cost:
  Pooled:       0 bytes (reused)
  Non-pooled:   64-128 bytes
```

### Network

```
Packet Rate (at 100k events/sec):
  Without packing: 100,000 packets/sec
  With packing:    5,000 packets/sec (20 events/packet)

Bandwidth (at 100k events/sec):
  Payload:   6-8 MB/sec
  Overhead:  140 KB/sec (5k packets × 28 bytes)
  Total:     ~8 MB/sec = 64 Mbps
```

---

## Configuration Examples

### High Throughput (>100k events/sec)

```go
config := &Config{
    BufferSize:      32768,      // Large buffer
    BatchSize:       100,        // Large batches
    MaxBatchAge:     50 * time.Millisecond,
    DropStrategy:    DropByPriority,
    EnablePooling:   true,
    EnableInterning: true,
    MaxMemoryMB:     20,
}
```

**Expected Performance:**
- Throughput: 200-500k events/sec
- Latency p99: 50-100ms
- Memory: 5-10MB
- Drop rate: <1%

---

### Low Latency (<50ms p99)

```go
config := &Config{
    BufferSize:      8192,       // Smaller buffer
    BatchSize:       20,         // Small batches
    MaxBatchAge:     20 * time.Millisecond,
    DropStrategy:    DropNewest,
    EnablePooling:   true,
    EnableInterning: false,
    MaxMemoryMB:     5,
}
```

**Expected Performance:**
- Throughput: 50-100k events/sec
- Latency p99: 20-40ms
- Memory: 2-3MB
- Drop rate: 2-5% under peak load

---

### Reliable (Minimize Drops)

```go
config := &Config{
    BufferSize:      65536,      // Very large buffer
    BatchSize:       50,
    MaxBatchAge:     100 * time.Millisecond,
    DropStrategy:    DropOldest, // Allow overwrite
    EnablePooling:   true,
    EnableInterning: true,
    MaxMemoryMB:     50,         // Large memory budget
    CircuitBreaker: &CircuitBreakerConfig{
        Enabled:          true,
        FailureThreshold: 10,
        Timeout:          30 * time.Second,
    },
}
```

**Expected Performance:**
- Throughput: 100-200k events/sec
- Latency p99: 100-200ms
- Memory: 10-20MB
- Drop rate: <0.1%

---

### Resource Constrained (<2MB memory)

```go
config := &Config{
    BufferSize:      4096,       // Small buffer
    BatchSize:       50,
    MaxBatchAge:     100 * time.Millisecond,
    DropStrategy:    DropSampled,
    SampleRate:      0.5,        // 50% sampling under pressure
    EnablePooling:   true,
    EnableInterning: false,      // Disabled to save memory
    MaxMemoryMB:     2,
}
```

**Expected Performance:**
- Throughput: 20-50k events/sec
- Latency p99: 100-200ms
- Memory: 1-2MB
- Drop rate: 5-10% under peak load

---

## Monitoring & Observability

### Key Metrics to Track

```
Throughput Metrics:
  stats.events.received         (counter)
  stats.events.sent             (counter)
  stats.events.dropped          (counter)
  stats.batches.sent            (counter)

Latency Metrics:
  stats.latency.buffer          (histogram)
  stats.latency.send            (histogram)
  stats.latency.e2e             (histogram)

Buffer Metrics:
  stats.buffer.size             (gauge)
  stats.buffer.utilization      (gauge, 0-1)
  stats.buffer.peak             (gauge)

Network Metrics:
  stats.udp.packets.sent        (counter)
  stats.udp.bytes.sent          (counter)
  stats.udp.errors              (counter)

Health Metrics:
  stats.health.status           (gauge, 0=unhealthy, 1=degraded, 2=healthy)
  stats.circuit.state           (gauge, 0=closed, 1=open, 2=half-open)
```

### Health Check Response

```json
{
  "healthy": true,
  "status": "healthy",
  "metrics": {
    "buffer_utilization": 0.42,
    "drop_rate": 0.003,
    "error_rate": 0.001,
    "p99_latency_ms": 67
  },
  "issues": []
}
```

---

## Failure Modes & Recovery

### Buffer Overflow

**Symptom:** Buffer utilization >90%, drop rate >1%

**Cause:** Events arriving faster than they can be sent

**Recovery:**
1. Adaptive batching increases batch size
2. Drop strategy activates (sampling/priority)
3. Health status → degraded
4. Alert fires if sustained >5 minutes

**Prevention:**
- Right-size buffer for expected peak load
- Monitor buffer utilization
- Test with 3x expected load

---

### Network Failure

**Symptom:** UDP send errors >5%, circuit breaker opens

**Cause:** Stats backend unreachable or network issue

**Recovery:**
1. Circuit breaker opens immediately
2. Events dropped (to prevent memory growth)
3. Test request every 30 seconds
4. Auto-recover when network restored
5. Health status → unhealthy

**Prevention:**
- Deploy redundant stats backends
- Monitor UDP error rate
- Configure circuit breaker appropriately

---

### Memory Pressure

**Symptom:** Memory usage growing unbounded

**Cause:** Memory leak or misconfiguration

**Recovery:**
1. Memory limiter activates
2. New allocations rejected
3. Drop rate increases
4. Alert fires
5. Health status → unhealthy

**Prevention:**
- Enable object pooling
- Set MaxMemoryMB limit
- Test with soak tests (24+ hours)
- Monitor memory growth rate

---

## Decision Matrix

### When to Use This Library

**Good Fit:**
- High-throughput applications (>10k events/sec)
- Non-blocking requirement (can't afford locks)
- UDP transport acceptable (some loss OK)
- Need performance guarantees

**Not a Good Fit:**
- Critical transactional data (use synchronous logging)
- Guaranteed delivery required (use message queue)
- Low throughput (<1k events/sec, overhead not justified)
- Rich querying needed (use database directly)

---

## Performance Optimization Priority

```
Priority 1 (Must Have):
  ✓ Lock-free ring buffer
  ✓ Object pooling
  ✓ Binary serialization
  ✓ Non-blocking API

Priority 2 (High Value):
  ✓ UDP packet packing
  ✓ Adaptive batching
  ✓ Drop strategies
  ✓ Internal metrics

Priority 3 (Nice to Have):
  ✓ String interning
  ✓ Circuit breaker
  ✓ Priority queues
  ✓ Advanced health checks
```

---

*Architecture Document v1.0*
*High-performance, non-blocking UDP stats library for Go*
