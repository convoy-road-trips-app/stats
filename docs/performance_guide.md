# Performance Optimization Guide for Non-Blocking Stats Library

## Table of Contents
1. [Buffering Strategies](#buffering-strategies)
2. [Batching Strategies](#batching-strategies)
3. [Backpressure Handling](#backpressure-handling)
4. [Memory Optimization](#memory-optimization)
5. [Self-Monitoring Metrics](#self-monitoring-metrics)
6. [UDP Packet Loss Handling](#udp-packet-loss-handling)
7. [Performance Benchmarking](#performance-benchmarking)

---

## 1. Buffering Strategies

### Lock-Free Ring Buffer (Recommended for High Throughput)

**Pattern: Single Producer, Single Consumer (SPSC) Ring Buffer**

```go
// RingBuffer provides lock-free buffering for stats events
type RingBuffer struct {
    buffer  []StatEvent
    mask    uint64          // size - 1, for fast modulo
    _pad0   [56]byte        // cache line padding
    head    uint64          // writer position
    _pad1   [56]byte        // prevent false sharing
    tail    uint64          // reader position
    _pad2   [56]byte
}

type StatEvent struct {
    Type      StatType
    Name      string
    Value     float64
    Tags      []Tag
    Timestamp int64
}

// NewRingBuffer creates a ring buffer with power-of-2 size
func NewRingBuffer(size int) *RingBuffer {
    // Ensure size is power of 2 for fast modulo
    if size&(size-1) != 0 {
        panic("size must be power of 2")
    }

    return &RingBuffer{
        buffer: make([]StatEvent, size),
        mask:   uint64(size - 1),
    }
}

// TryPush attempts to push without blocking (returns false if full)
func (rb *RingBuffer) TryPush(event StatEvent) bool {
    head := atomic.LoadUint64(&rb.head)
    tail := atomic.LoadUint64(&rb.tail)

    // Check if buffer is full
    if head-tail >= uint64(len(rb.buffer)) {
        return false // Buffer full
    }

    // Write to buffer
    rb.buffer[head&rb.mask] = event

    // Commit write
    atomic.StoreUint64(&rb.head, head+1)
    return true
}

// TryPop attempts to pop without blocking (returns false if empty)
func (rb *RingBuffer) TryPop() (StatEvent, bool) {
    head := atomic.LoadUint64(&rb.head)
    tail := atomic.LoadUint64(&rb.tail)

    // Check if buffer is empty
    if tail >= head {
        return StatEvent{}, false
    }

    // Read from buffer
    event := rb.buffer[tail&rb.mask]

    // Commit read
    atomic.StoreUint64(&rb.tail, tail+1)
    return event, true
}

// Size returns current buffer utilization
func (rb *RingBuffer) Size() int {
    head := atomic.LoadUint64(&rb.head)
    tail := atomic.LoadUint64(&rb.tail)
    return int(head - tail)
}

// Capacity returns total buffer capacity
func (rb *RingBuffer) Capacity() int {
    return len(rb.buffer)
}
```

**Performance Characteristics:**
- Zero allocations per operation
- Lock-free for SPSC (single producer, single consumer)
- O(1) push/pop operations
- Cache-line padding prevents false sharing
- ~10-20ns per operation on modern CPUs

**When to Use:**
- High-throughput scenarios (>100k ops/sec)
- Predictable memory footprint required
- Single writer, single reader pattern

---

### Bounded Channel (Simpler Alternative)

**Pattern: Multi-Producer, Single Consumer (MPSC)**

```go
type ChannelBuffer struct {
    events chan StatEvent
    size   int
}

func NewChannelBuffer(size int) *ChannelBuffer {
    return &ChannelBuffer{
        events: make(chan StatEvent, size),
        size:   size,
    }
}

// TryPush with timeout to prevent blocking
func (cb *ChannelBuffer) TryPush(event StatEvent, timeout time.Duration) bool {
    select {
    case cb.events <- event:
        return true
    case <-time.After(timeout):
        return false
    default: // Non-blocking attempt
        return false
    }
}

// Pop blocks until event available
func (cb *ChannelBuffer) Pop(ctx context.Context) (StatEvent, bool) {
    select {
    case event := <-cb.events:
        return event, true
    case <-ctx.Done():
        return StatEvent{}, false
    }
}

func (cb *ChannelBuffer) Size() int {
    return len(cb.events)
}
```

**Performance Characteristics:**
- ~100-200ns per operation (10x slower than ring buffer)
- Thread-safe for multiple producers
- Built-in Go runtime support
- Simpler to implement and maintain

**When to Use:**
- Multiple writers from different goroutines
- Moderate throughput (<100k ops/sec)
- Prefer simplicity over raw performance

---

### Hybrid: Sharded Buffers for MPSC

**Pattern: Reduce contention with per-CPU buffers**

```go
type ShardedBuffer struct {
    shards    []*RingBuffer
    shardMask uint64
}

func NewShardedBuffer(numShards, sizePerShard int) *ShardedBuffer {
    if numShards&(numShards-1) != 0 {
        panic("numShards must be power of 2")
    }

    shards := make([]*RingBuffer, numShards)
    for i := range shards {
        shards[i] = NewRingBuffer(sizePerShard)
    }

    return &ShardedBuffer{
        shards:    shards,
        shardMask: uint64(numShards - 1),
    }
}

// TryPush distributes across shards using goroutine ID or random
func (sb *ShardedBuffer) TryPush(event StatEvent) bool {
    // Use goroutine ID or random to select shard
    shardIdx := runtime_fastrand() & sb.shardMask
    return sb.shards[shardIdx].TryPush(event)
}

// Collector drains all shards
func (sb *ShardedBuffer) DrainAll(batch []StatEvent) []StatEvent {
    batch = batch[:0]
    for _, shard := range sb.shards {
        for {
            event, ok := shard.TryPop()
            if !ok {
                break
            }
            batch = append(batch, event)
        }
    }
    return batch
}
```

**Performance Characteristics:**
- Reduces contention in multi-producer scenarios
- Near ring-buffer performance per operation
- Slightly more complex drain logic

**Sizing Recommendations:**
- **Ring Buffer Size:** 8192-16384 events (power of 2)
  - At 64 bytes/event = 512KB-1MB memory
- **Sharded Buffer:** NumCPU shards, 4096 events each
  - Total: NumCPU * 4096 * 64 bytes
- **Channel Buffer:** 4096-8192 events

---

## 2. Batching Strategies

### Fixed-Size Batching

**Pattern: Accumulate N events before transmission**

```go
type BatchProcessor struct {
    buffer       *RingBuffer
    batchSize    int
    maxBatchAge  time.Duration
    sender       StatsSender

    // Pre-allocated batch buffer
    batch        []StatEvent
    batchBytes   []byte

    // Metrics
    batchesSent  uint64
    eventsSent   uint64
    lastSendTime time.Time
}

func NewBatchProcessor(buffer *RingBuffer, batchSize int, maxAge time.Duration) *BatchProcessor {
    return &BatchProcessor{
        buffer:      buffer,
        batchSize:   batchSize,
        maxBatchAge: maxAge,
        batch:       make([]StatEvent, 0, batchSize),
        batchBytes:  make([]byte, 0, batchSize*128), // Estimate 128 bytes/event
        lastSendTime: time.Now(),
    }
}

func (bp *BatchProcessor) Run(ctx context.Context) {
    ticker := time.NewTicker(bp.maxBatchAge / 10) // Check 10x per max age
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            bp.flush() // Final flush
            return

        case <-ticker.C:
            // Time-based flush
            if len(bp.batch) > 0 && time.Since(bp.lastSendTime) >= bp.maxBatchAge {
                bp.flush()
            }

        default:
            // Drain buffer into batch
            event, ok := bp.buffer.TryPop()
            if !ok {
                if len(bp.batch) > 0 && time.Since(bp.lastSendTime) >= bp.maxBatchAge {
                    bp.flush()
                }
                time.Sleep(100 * time.Microsecond) // Brief pause
                continue
            }

            bp.batch = append(bp.batch, event)

            // Size-based flush
            if len(bp.batch) >= bp.batchSize {
                bp.flush()
            }
        }
    }
}

func (bp *BatchProcessor) flush() {
    if len(bp.batch) == 0 {
        return
    }

    // Serialize batch to bytes (reuse buffer)
    bp.batchBytes = bp.serializeBatch(bp.batch, bp.batchBytes[:0])

    // Send (non-blocking)
    if err := bp.sender.SendBatch(bp.batchBytes); err != nil {
        // Log error, increment drop counter
        atomic.AddUint64(&bp.sender.droppedBatches, 1)
    } else {
        atomic.AddUint64(&bp.batchesSent, 1)
        atomic.AddUint64(&bp.eventsSent, uint64(len(bp.batch)))
    }

    bp.batch = bp.batch[:0] // Reset but keep capacity
    bp.lastSendTime = time.Now()
}
```

**Batch Size Recommendations:**
- **Ethernet MTU:** 1500 bytes (safe for single UDP packet)
- **Jumbo Frames:** 9000 bytes (if network supports)
- **Events per batch:** 10-50 events (depending on event size)
- **Max age:** 100ms-1s (balance latency vs efficiency)

---

### Adaptive Batching

**Pattern: Adjust batch size based on load**

```go
type AdaptiveBatcher struct {
    minBatchSize int
    maxBatchSize int
    currentSize  int

    // Adaptive parameters
    highWatermark int // Start increasing batch size
    lowWatermark  int // Start decreasing batch size

    buffer *RingBuffer
}

func (ab *AdaptiveBatcher) adjustBatchSize() {
    bufferSize := ab.buffer.Size()
    capacity := ab.buffer.Capacity()
    utilization := float64(bufferSize) / float64(capacity)

    switch {
    case utilization > 0.75: // High pressure
        ab.currentSize = ab.maxBatchSize

    case utilization > 0.50: // Medium pressure
        ab.currentSize = (ab.minBatchSize + ab.maxBatchSize) / 2

    case utilization < 0.25: // Low pressure
        ab.currentSize = ab.minBatchSize
    }
}

func (ab *AdaptiveBatcher) shouldFlush(batchLen int) bool {
    return batchLen >= ab.currentSize
}
```

**Benefits:**
- Low latency during quiet periods (small batches)
- High throughput during bursts (large batches)
- Automatic tuning reduces configuration complexity

---

### UDP Packet Packing

**Pattern: Maximize bytes per packet**

```go
type UDPPacketPacker struct {
    maxPacketSize int // Typically 1472 bytes (1500 - IP/UDP headers)

    // Serialization buffer
    packet []byte
}

const (
    // UDP/IP overhead
    IPv4HeaderSize = 20
    UDPHeaderSize  = 8
    EthernetMTU    = 1500
    SafeUDPPayload = EthernetMTU - IPv4HeaderSize - UDPHeaderSize // 1472
)

func NewUDPPacketPacker() *UDPPacketPacker {
    return &UDPPacketPacker{
        maxPacketSize: SafeUDPPayload,
        packet:        make([]byte, 0, SafeUDPPayload),
    }
}

// PackEvents packs as many events as possible into single packet
func (upp *UDPPacketPacker) PackEvents(events []StatEvent) [][]byte {
    packets := make([][]byte, 0)
    upp.packet = upp.packet[:0]

    for _, event := range events {
        serialized := upp.serializeEvent(event)

        // Check if adding this event exceeds packet size
        if len(upp.packet)+len(serialized) > upp.maxPacketSize {
            // Finalize current packet
            packets = append(packets, append([]byte(nil), upp.packet...))
            upp.packet = upp.packet[:0]
        }

        upp.packet = append(upp.packet, serialized...)
    }

    // Add final packet if any data remains
    if len(upp.packet) > 0 {
        packets = append(packets, upp.packet)
    }

    return packets
}

// serializeEvent uses efficient binary encoding
func (upp *UDPPacketPacker) serializeEvent(event StatEvent) []byte {
    // Use MessagePack, Protocol Buffers, or custom binary format
    // Avoid JSON in hot path (3-10x slower)

    // Example with binary encoding:
    buf := make([]byte, 0, 128)
    buf = append(buf, byte(event.Type))
    buf = appendString(buf, event.Name)
    buf = appendFloat64(buf, event.Value)
    buf = appendInt64(buf, event.Timestamp)
    // ... encode tags
    return buf
}
```

**Serialization Performance:**

| Format | Size (bytes) | Encode (ns/op) | Decode (ns/op) |
|--------|-------------|----------------|----------------|
| JSON | 180 | 1200 | 2800 |
| MessagePack | 95 | 450 | 650 |
| Protocol Buffers | 68 | 280 | 320 |
| Custom Binary | 62 | 120 | 140 |

**Recommendation:** Use Protocol Buffers for balance of performance and maintainability.

---

## 3. Backpressure Handling

### Drop Strategies

**Pattern: Define behavior when buffer is full**

```go
type DropStrategy int

const (
    DropOldest  DropStrategy = iota // Drop oldest events (ring buffer overwrites)
    DropNewest                       // Drop incoming events (preserve history)
    DropByPriority                   // Drop low-priority events first
    DropSampled                      // Statistical sampling
)

type StatsClient struct {
    buffer       *RingBuffer
    dropStrategy DropStrategy

    // Metrics
    eventsReceived uint64
    eventsDropped  uint64
    eventsSent     uint64

    // Sampling
    sampleRate    float64 // 0.0 to 1.0
    sampleCounter uint64
}

func (sc *StatsClient) RecordMetric(name string, value float64, tags ...Tag) {
    atomic.AddUint64(&sc.eventsReceived, 1)

    event := StatEvent{
        Type:      MetricType,
        Name:      name,
        Value:     value,
        Tags:      tags,
        Timestamp: time.Now().UnixNano(),
    }

    // Apply backpressure strategy
    if !sc.tryPushWithStrategy(event) {
        atomic.AddUint64(&sc.eventsDropped, 1)
    }
}

func (sc *StatsClient) tryPushWithStrategy(event StatEvent) bool {
    switch sc.dropStrategy {
    case DropNewest:
        return sc.buffer.TryPush(event)

    case DropOldest:
        // Force push, overwriting oldest if full
        for !sc.buffer.TryPush(event) {
            // Pop and discard oldest
            if _, ok := sc.buffer.TryPop(); !ok {
                return false // Shouldn't happen
            }
            atomic.AddUint64(&sc.eventsDropped, 1)
        }
        return true

    case DropSampled:
        // Under pressure, use sampling
        bufferUtil := float64(sc.buffer.Size()) / float64(sc.buffer.Capacity())
        if bufferUtil > 0.75 {
            // Sample to reduce load
            sample := atomic.AddUint64(&sc.sampleCounter, 1)
            if float64(sample%100) >= sc.sampleRate*100 {
                return false // Drop this sample
            }
        }
        return sc.buffer.TryPush(event)

    case DropByPriority:
        return sc.priorityDrop(event)

    default:
        return sc.buffer.TryPush(event)
    }
}

func (sc *StatsClient) priorityDrop(event StatEvent) bool {
    // If buffer is full, drop low-priority events
    if sc.buffer.Size() >= sc.buffer.Capacity()*9/10 { // 90% full
        if event.Type == DebugType || event.Type == InfoType {
            return false // Drop low-priority
        }
    }
    return sc.buffer.TryPush(event)
}
```

### Priority Queues

**Pattern: Critical stats always get through**

```go
type PriorityBuffer struct {
    highPriority *RingBuffer // Errors, critical metrics
    medPriority  *RingBuffer // Normal metrics
    lowPriority  *RingBuffer // Debug, verbose stats
}

func NewPriorityBuffer(highSize, medSize, lowSize int) *PriorityBuffer {
    return &PriorityBuffer{
        highPriority: NewRingBuffer(highSize),
        medPriority:  NewRingBuffer(medSize),
        lowPriority:  NewRingBuffer(lowSize),
    }
}

func (pb *PriorityBuffer) Push(event StatEvent) bool {
    switch event.Priority {
    case HighPriority:
        return pb.highPriority.TryPush(event)
    case MediumPriority:
        return pb.medPriority.TryPush(event)
    case LowPriority:
        return pb.lowPriority.TryPush(event)
    default:
        return pb.medPriority.TryPush(event)
    }
}

func (pb *PriorityBuffer) DrainBatch(maxEvents int) []StatEvent {
    batch := make([]StatEvent, 0, maxEvents)

    // Drain high priority first
    for len(batch) < maxEvents {
        event, ok := pb.highPriority.TryPop()
        if !ok {
            break
        }
        batch = append(batch, event)
    }

    // Then medium priority
    for len(batch) < maxEvents {
        event, ok := pb.medPriority.TryPop()
        if !ok {
            break
        }
        batch = append(batch, event)
    }

    // Finally low priority (if space remains)
    for len(batch) < maxEvents {
        event, ok := pb.lowPriority.TryPop()
        if !ok {
            break
        }
        batch = append(batch, event)
    }

    return batch
}
```

### Circuit Breaker

**Pattern: Stop accepting stats when downstream is failing**

```go
type CircuitBreaker struct {
    state            CircuitState
    failureThreshold int
    successThreshold int
    timeout          time.Duration

    failures  int
    successes int
    lastStateChange time.Time

    mu sync.RWMutex
}

type CircuitState int

const (
    StateClosed   CircuitState = iota // Normal operation
    StateOpen                          // Failing, reject requests
    StateHalfOpen                      // Testing recovery
)

func (cb *CircuitBreaker) Call(fn func() error) error {
    if !cb.canAttempt() {
        return ErrCircuitOpen
    }

    err := fn()
    cb.recordResult(err)
    return err
}

func (cb *CircuitBreaker) canAttempt() bool {
    cb.mu.RLock()
    defer cb.mu.RUnlock()

    switch cb.state {
    case StateClosed:
        return true
    case StateOpen:
        // Check if timeout has passed
        if time.Since(cb.lastStateChange) > cb.timeout {
            cb.mu.RUnlock()
            cb.mu.Lock()
            cb.state = StateHalfOpen
            cb.mu.Unlock()
            cb.mu.RLock()
            return true
        }
        return false
    case StateHalfOpen:
        return true
    }
    return false
}

func (cb *CircuitBreaker) recordResult(err error) {
    cb.mu.Lock()
    defer cb.mu.Unlock()

    if err != nil {
        cb.failures++
        cb.successes = 0

        if cb.failures >= cb.failureThreshold {
            cb.state = StateOpen
            cb.lastStateChange = time.Now()
        }
    } else {
        cb.failures = 0
        cb.successes++

        if cb.state == StateHalfOpen && cb.successes >= cb.successThreshold {
            cb.state = StateClosed
            cb.lastStateChange = time.Now()
        }
    }
}
```

**Backpressure Strategy Selection:**

| Scenario | Strategy | Rationale |
|----------|----------|-----------|
| Real-time monitoring | DropOldest | Fresh data more valuable |
| Historical analysis | DropNewest | Preserve existing data |
| Mixed workload | DropByPriority | Protect critical metrics |
| High cardinality | DropSampled | Statistical accuracy maintained |
| Unreliable network | CircuitBreaker | Prevent resource exhaustion |

---

## 4. Memory Optimization

### Object Pooling

**Pattern: Reuse allocations to reduce GC pressure**

```go
var eventPool = sync.Pool{
    New: func() interface{} {
        return &StatEvent{
            Tags: make([]Tag, 0, 8), // Pre-allocate common size
        }
    },
}

var tagPool = sync.Pool{
    New: func() interface{} {
        tags := make([]Tag, 0, 8)
        return &tags
    },
}

var byteBufferPool = sync.Pool{
    New: func() interface{} {
        buf := make([]byte, 0, 2048)
        return &buf
    },
}

func acquireEvent() *StatEvent {
    return eventPool.Get().(*StatEvent)
}

func releaseEvent(event *StatEvent) {
    // Reset event
    event.Type = 0
    event.Name = ""
    event.Value = 0
    event.Tags = event.Tags[:0]
    event.Timestamp = 0

    eventPool.Put(event)
}

func acquireByteBuffer() *[]byte {
    return byteBufferPool.Get().(*[]byte)
}

func releaseByteBuffer(buf *[]byte) {
    *buf = (*buf)[:0] // Reset length but keep capacity
    byteBufferPool.Put(buf)
}
```

**Memory Savings:**
- Reduces allocations by 80-95%
- Decreases GC pause times by 60-80%
- Critical for >10k events/sec throughput

---

### String Interning

**Pattern: Deduplicate repeated metric names and tags**

```go
type StringInterner struct {
    strings sync.Map // map[string]string
}

func (si *StringInterner) Intern(s string) string {
    if existing, ok := si.strings.Load(s); ok {
        return existing.(string)
    }

    si.strings.Store(s, s)
    return s
}

// Usage in stats client
type StatsClient struct {
    interner *StringInterner
}

func (sc *StatsClient) RecordMetric(name string, value float64) {
    // Intern common strings to reduce memory
    internedName := sc.interner.Intern(name)

    event := acquireEvent()
    event.Name = internedName
    event.Value = value

    sc.buffer.TryPush(*event)
}
```

**Memory Impact:**
- Typical stats workload: 1000 unique metric names
- Without interning: 1M events × 50 bytes/name = 50MB
- With interning: 1M events × 8 bytes/pointer + 1000 × 50 bytes = 8.05MB
- **Savings: 84%**

---

### Zero-Copy Serialization

**Pattern: Serialize directly into UDP send buffer**

```go
type ZeroCopySerializer struct {
    udpConn *net.UDPConn
    sendBuf []byte // Pre-allocated send buffer
}

func (zcs *ZeroCopySerializer) SendBatch(events []StatEvent) error {
    // Serialize directly into send buffer
    offset := 0

    for _, event := range events {
        // Check remaining space
        if offset+128 > len(zcs.sendBuf) {
            // Send current buffer
            if err := zcs.flush(offset); err != nil {
                return err
            }
            offset = 0
        }

        // Serialize in-place
        offset += zcs.serializeEventAt(event, zcs.sendBuf[offset:])
    }

    // Send remaining data
    if offset > 0 {
        return zcs.flush(offset)
    }

    return nil
}

func (zcs *ZeroCopySerializer) flush(length int) error {
    _, err := zcs.udpConn.Write(zcs.sendBuf[:length])
    return err
}

func (zcs *ZeroCopySerializer) serializeEventAt(event StatEvent, buf []byte) int {
    offset := 0

    // Use unsafe for zero-copy numeric conversions
    buf[offset] = byte(event.Type)
    offset++

    // Variable-length encoding for name
    nameLen := len(event.Name)
    binary.LittleEndian.PutUint16(buf[offset:], uint16(nameLen))
    offset += 2
    copy(buf[offset:], event.Name)
    offset += nameLen

    // Fixed-size value
    binary.LittleEndian.PutUint64(buf[offset:], math.Float64bits(event.Value))
    offset += 8

    // ... encode remaining fields

    return offset
}
```

---

### Memory Limits and Monitoring

```go
type MemoryLimiter struct {
    maxBytes    int64
    currentBytes int64
}

func (ml *MemoryLimiter) TryAllocate(size int64) bool {
    current := atomic.LoadInt64(&ml.currentBytes)
    if current+size > ml.maxBytes {
        return false // Allocation would exceed limit
    }

    atomic.AddInt64(&ml.currentBytes, size)
    return true
}

func (ml *MemoryLimiter) Release(size int64) {
    atomic.AddInt64(&ml.currentBytes, -size)
}

func (ml *MemoryLimiter) Usage() float64 {
    current := atomic.LoadInt64(&ml.currentBytes)
    return float64(current) / float64(ml.maxBytes)
}
```

**Memory Budget Recommendations:**

| Throughput | Buffer Size | String Cache | Total Memory |
|------------|-------------|--------------|--------------|
| 10k/sec | 8K events × 64B = 512KB | 1K strings × 50B = 50KB | ~1MB |
| 100k/sec | 16K events × 64B = 1MB | 5K strings × 50B = 250KB | ~2MB |
| 1M/sec | 32K events × 64B = 2MB | 10K strings × 50B = 500KB | ~5MB |

---

## 5. Self-Monitoring Metrics

### Internal Metrics Collection

```go
type InternalMetrics struct {
    // Throughput
    EventsReceived   uint64
    EventsBuffered   uint64
    EventsSent       uint64
    EventsDropped    uint64
    BatchesSent      uint64

    // Latency
    BufferLatency    Histogram // Time in buffer
    SendLatency      Histogram // Time to send
    E2ELatency       Histogram // End-to-end

    // Buffer health
    BufferSize       int64
    BufferCapacity   int64
    BufferPeakSize   int64

    // Network
    UDPSendErrors    uint64
    UDPSendBytes     uint64
    UDPPacketsSent   uint64

    // Memory
    AllocatedBytes   int64
    PoolHits         uint64
    PoolMisses       uint64

    // Backpressure
    BackpressureEvents uint64
    CircuitBreakerOpen uint64
    SamplingActive     bool
    CurrentSampleRate  float64
}

type Histogram struct {
    mu     sync.RWMutex
    counts [20]uint64 // Buckets: <1ms, <2ms, <5ms, <10ms, ...
}

func (h *Histogram) Record(duration time.Duration) {
    ms := duration.Milliseconds()
    bucket := h.selectBucket(ms)

    h.mu.Lock()
    h.counts[bucket]++
    h.mu.Unlock()
}

func (h *Histogram) Percentile(p float64) time.Duration {
    h.mu.RLock()
    defer h.mu.RUnlock()

    total := uint64(0)
    for _, count := range h.counts {
        total += count
    }

    target := uint64(float64(total) * p)
    sum := uint64(0)

    for i, count := range h.counts {
        sum += count
        if sum >= target {
            return time.Duration(h.bucketValue(i)) * time.Millisecond
        }
    }

    return 0
}
```

### Health Check Endpoint

```go
type HealthChecker struct {
    metrics  *InternalMetrics
    client   *StatsClient
}

type HealthStatus struct {
    Healthy       bool              `json:"healthy"`
    Status        string            `json:"status"`
    Metrics       map[string]interface{} `json:"metrics"`
    Issues        []string          `json:"issues,omitempty"`
}

func (hc *HealthChecker) Check() HealthStatus {
    status := HealthStatus{
        Healthy: true,
        Status:  "healthy",
        Metrics: make(map[string]interface{}),
        Issues:  make([]string, 0),
    }

    // Check buffer utilization
    bufferUtil := float64(hc.metrics.BufferSize) / float64(hc.metrics.BufferCapacity)
    status.Metrics["buffer_utilization"] = bufferUtil

    if bufferUtil > 0.90 {
        status.Healthy = false
        status.Status = "degraded"
        status.Issues = append(status.Issues, "Buffer >90% full")
    }

    // Check drop rate
    dropRate := float64(hc.metrics.EventsDropped) / float64(hc.metrics.EventsReceived)
    status.Metrics["drop_rate"] = dropRate

    if dropRate > 0.01 { // >1% drops
        status.Healthy = false
        status.Status = "degraded"
        status.Issues = append(status.Issues, fmt.Sprintf("Drop rate: %.2f%%", dropRate*100))
    }

    // Check send errors
    errorRate := float64(hc.metrics.UDPSendErrors) / float64(hc.metrics.UDPPacketsSent)
    status.Metrics["error_rate"] = errorRate

    if errorRate > 0.05 { // >5% errors
        status.Healthy = false
        status.Status = "unhealthy"
        status.Issues = append(status.Issues, "High UDP send error rate")
    }

    // Latency checks
    p99Latency := hc.metrics.E2ELatency.Percentile(0.99)
    status.Metrics["p99_latency_ms"] = p99Latency.Milliseconds()

    if p99Latency > 5*time.Second {
        status.Issues = append(status.Issues, "High P99 latency")
    }

    return status
}
```

### Metrics Export

```go
// Expose internal metrics periodically
func (sc *StatsClient) ExportMetrics() map[string]interface{} {
    return map[string]interface{}{
        "throughput": map[string]uint64{
            "events_received": atomic.LoadUint64(&sc.metrics.EventsReceived),
            "events_sent":     atomic.LoadUint64(&sc.metrics.EventsSent),
            "events_dropped":  atomic.LoadUint64(&sc.metrics.EventsDropped),
            "batches_sent":    atomic.LoadUint64(&sc.metrics.BatchesSent),
        },
        "buffer": map[string]interface{}{
            "size":     sc.buffer.Size(),
            "capacity": sc.buffer.Capacity(),
            "utilization": float64(sc.buffer.Size()) / float64(sc.buffer.Capacity()),
        },
        "latency": map[string]int64{
            "p50_ms": sc.metrics.E2ELatency.Percentile(0.50).Milliseconds(),
            "p95_ms": sc.metrics.E2ELatency.Percentile(0.95).Milliseconds(),
            "p99_ms": sc.metrics.E2ELatency.Percentile(0.99).Milliseconds(),
        },
        "network": map[string]uint64{
            "packets_sent": atomic.LoadUint64(&sc.metrics.UDPPacketsSent),
            "bytes_sent":   atomic.LoadUint64(&sc.metrics.UDPSendBytes),
            "send_errors":  atomic.LoadUint64(&sc.metrics.UDPSendErrors),
        },
    }
}
```

**Key Metrics to Monitor:**

| Metric | Threshold | Action |
|--------|-----------|--------|
| Buffer utilization | >80% | Increase batch size or send frequency |
| Drop rate | >1% | Increase buffer size or implement sampling |
| P99 latency | >1s | Investigate backlog, reduce batch age |
| UDP send errors | >5% | Check network, implement circuit breaker |
| Memory growth | Unbounded | Check for leaks, enable pooling |

---

## 6. UDP Packet Loss Handling

### Loss Detection

```go
type SequenceTracker struct {
    sequence uint64
}

func (st *SequenceTracker) Next() uint64 {
    return atomic.AddUint64(&st.sequence, 1)
}

// On sender side
type UDPSender struct {
    conn     *net.UDPConn
    tracker  *SequenceTracker
}

func (us *UDPSender) SendBatch(events []StatEvent) error {
    packet := &Packet{
        Sequence:  us.tracker.Next(),
        Timestamp: time.Now().UnixNano(),
        Events:    events,
    }

    data := serializePacket(packet)
    _, err := us.conn.Write(data)
    return err
}

// On receiver side (stats backend)
type PacketReceiver struct {
    lastSequence uint64
    packetsLost  uint64
    packetsRecv  uint64
}

func (pr *PacketReceiver) ProcessPacket(packet *Packet) {
    expected := atomic.LoadUint64(&pr.lastSequence) + 1

    if packet.Sequence != expected {
        gap := packet.Sequence - expected
        atomic.AddUint64(&pr.packetsLost, gap)
        // Log warning about packet loss
    }

    atomic.StoreUint64(&pr.lastSequence, packet.Sequence)
    atomic.AddUint64(&pr.packetsRecv, 1)
}
```

### Loss Mitigation Strategies

**1. Redundant Transmission (for critical metrics)**

```go
type RedundantSender struct {
    conn1 *net.UDPConn
    conn2 *net.UDPConn // Backup destination
}

func (rs *RedundantSender) SendCritical(event StatEvent) error {
    data := serialize(event)

    // Send to both destinations
    err1 := rs.send(rs.conn1, data)
    err2 := rs.send(rs.conn2, data)

    if err1 != nil && err2 != nil {
        return fmt.Errorf("both sends failed")
    }
    return nil
}
```

**2. Aggregation (reduce packet count)**

```go
type LocalAggregator struct {
    counters map[string]*int64
    gauges   map[string]*float64
    mu       sync.RWMutex
}

func (la *LocalAggregator) IncrementCounter(name string, value int64) {
    la.mu.RLock()
    counter, exists := la.counters[name]
    la.mu.RUnlock()

    if exists {
        atomic.AddInt64(counter, value)
        return
    }

    // Create new counter
    la.mu.Lock()
    newCounter := new(int64)
    atomic.StoreInt64(newCounter, value)
    la.counters[name] = newCounter
    la.mu.Unlock()
}

func (la *LocalAggregator) Flush() []StatEvent {
    la.mu.Lock()
    defer la.mu.Unlock()

    events := make([]StatEvent, 0, len(la.counters)+len(la.gauges))

    for name, counter := range la.counters {
        value := atomic.LoadInt64(counter)
        events = append(events, StatEvent{
            Type:  CounterType,
            Name:  name,
            Value: float64(value),
        })
        atomic.StoreInt64(counter, 0) // Reset
    }

    // Similar for gauges...

    return events
}
```

**3. Checksum Validation**

```go
func (packet *Packet) AddChecksum() {
    data := packet.serialize()
    packet.Checksum = crc32.ChecksumIEEE(data)
}

func (packet *Packet) ValidateChecksum() bool {
    expected := packet.Checksum
    packet.Checksum = 0
    data := packet.serialize()
    actual := crc32.ChecksumIEEE(data)
    packet.Checksum = expected
    return expected == actual
}
```

### Acceptable Loss Rates

| Metric Type | Acceptable Loss | Mitigation |
|-------------|-----------------|------------|
| Counters | <5% | Local aggregation, periodic resync |
| Gauges | <2% | Latest value semantics, redundancy |
| Timing | <1% | Sampling already applied |
| Critical Events | 0% | Redundant send, acknowledgment |

---

## 7. Performance Benchmarking

### Benchmark Structure

```go
// benchmark_test.go
package stats

import (
    "testing"
    "time"
)

func BenchmarkRingBufferPush(b *testing.B) {
    rb := NewRingBuffer(8192)
    event := StatEvent{
        Type:  MetricType,
        Name:  "test.metric",
        Value: 123.45,
        Tags:  []Tag{{"env", "prod"}},
    }

    b.ResetTimer()
    b.ReportAllocs()

    for i := 0; i < b.N; i++ {
        rb.TryPush(event)
    }

    b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "ops/sec")
}

func BenchmarkChannelPush(b *testing.B) {
    ch := NewChannelBuffer(8192)
    event := StatEvent{
        Type:  MetricType,
        Name:  "test.metric",
        Value: 123.45,
    }

    b.ResetTimer()
    b.ReportAllocs()

    for i := 0; i < b.N; i++ {
        ch.TryPush(event, 0)
    }

    b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "ops/sec")
}

func BenchmarkEndToEnd(b *testing.B) {
    client := NewStatsClient(&Config{
        BufferSize:    8192,
        BatchSize:     50,
        MaxBatchAge:   100 * time.Millisecond,
        UDPAddress:    "localhost:8125",
    })
    defer client.Close()

    b.ResetTimer()
    b.ReportAllocs()
    b.RunParallel(func(pb *testing.PB) {
        for pb.Next() {
            client.RecordMetric("benchmark.metric", 1.0)
        }
    })

    b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "ops/sec")
}

func BenchmarkSerialization(b *testing.B) {
    event := StatEvent{
        Type:      MetricType,
        Name:      "test.metric",
        Value:     123.45,
        Tags:      []Tag{{"env", "prod"}, {"host", "server1"}},
        Timestamp: time.Now().UnixNano(),
    }

    serializers := map[string]Serializer{
        "JSON":        &JSONSerializer{},
        "MessagePack": &MessagePackSerializer{},
        "Protobuf":    &ProtobufSerializer{},
        "Binary":      &BinarySerializer{},
    }

    for name, serializer := range serializers {
        b.Run(name, func(b *testing.B) {
            b.ReportAllocs()
            for i := 0; i < b.N; i++ {
                _ = serializer.Serialize(event)
            }
        })
    }
}
```

### Load Testing

```go
// loadtest.go
package main

import (
    "context"
    "fmt"
    "sync"
    "sync/atomic"
    "time"
)

type LoadTest struct {
    client         *StatsClient
    duration       time.Duration
    concurrency    int
    targetRate     int // events per second

    eventsGenerated uint64
    eventsDropped   uint64
}

func (lt *LoadTest) Run() {
    ctx, cancel := context.WithTimeout(context.Background(), lt.duration)
    defer cancel()

    var wg sync.WaitGroup
    eventsPerWorker := lt.targetRate / lt.concurrency
    interval := time.Second / time.Duration(eventsPerWorker)

    // Start workers
    for i := 0; i < lt.concurrency; i++ {
        wg.Add(1)
        go lt.worker(ctx, &wg, interval, i)
    }

    // Monitor progress
    go lt.monitor(ctx)

    wg.Wait()
    lt.printResults()
}

func (lt *LoadTest) worker(ctx context.Context, wg *sync.WaitGroup, interval time.Duration, id int) {
    defer wg.Done()

    ticker := time.NewTicker(interval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            atomic.AddUint64(&lt.eventsGenerated, 1)

            if !lt.client.RecordMetric(
                fmt.Sprintf("load.test.worker.%d", id),
                float64(time.Now().UnixNano()),
            ) {
                atomic.AddUint64(&lt.eventsDropped, 1)
            }
        }
    }
}

func (lt *LoadTest) monitor(ctx context.Context) {
    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()

    lastGenerated := uint64(0)

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            generated := atomic.LoadUint64(&lt.eventsGenerated)
            dropped := atomic.LoadUint64(&lt.eventsDropped)

            rate := generated - lastGenerated
            dropRate := float64(dropped) / float64(generated) * 100

            metrics := lt.client.ExportMetrics()
            bufferUtil := metrics["buffer"].(map[string]interface{})["utilization"].(float64)

            fmt.Printf("[%s] Rate: %d/s, Dropped: %.2f%%, Buffer: %.1f%%\n",
                time.Now().Format("15:04:05"),
                rate,
                dropRate,
                bufferUtil*100,
            )

            lastGenerated = generated
        }
    }
}

func (lt *LoadTest) printResults() {
    generated := atomic.LoadUint64(&lt.eventsGenerated)
    dropped := atomic.LoadUint64(&lt.eventsDropped)

    fmt.Printf("\n=== Load Test Results ===\n")
    fmt.Printf("Duration: %s\n", lt.duration)
    fmt.Printf("Target Rate: %d events/sec\n", lt.targetRate)
    fmt.Printf("Events Generated: %d\n", generated)
    fmt.Printf("Events Dropped: %d (%.2f%%)\n", dropped, float64(dropped)/float64(generated)*100)
    fmt.Printf("Effective Rate: %.0f events/sec\n", float64(generated)/lt.duration.Seconds())

    metrics := lt.client.ExportMetrics()
    fmt.Printf("\nClient Metrics:\n")
    fmt.Printf("  Events Sent: %d\n", metrics["throughput"].(map[string]uint64)["events_sent"])
    fmt.Printf("  Batches Sent: %d\n", metrics["throughput"].(map[string]uint64)["batches_sent"])
    fmt.Printf("  P99 Latency: %dms\n", metrics["latency"].(map[string]int64)["p99_ms"])
    fmt.Printf("  UDP Errors: %d\n", metrics["network"].(map[string]uint64)["send_errors"])
}

// Example usage
func main() {
    client := NewStatsClient(&Config{
        BufferSize:  16384,
        BatchSize:   50,
        MaxBatchAge: 100 * time.Millisecond,
        UDPAddress:  "localhost:8125",
    })
    defer client.Close()

    tests := []struct {
        name        string
        targetRate  int
        concurrency int
        duration    time.Duration
    }{
        {"Baseline", 10_000, 4, 30 * time.Second},
        {"High Load", 100_000, 8, 60 * time.Second},
        {"Stress", 500_000, 16, 30 * time.Second},
        {"Burst", 1_000_000, 32, 10 * time.Second},
    }

    for _, test := range tests {
        fmt.Printf("\n\n=== Running: %s ===\n", test.name)

        lt := &LoadTest{
            client:      client,
            duration:    test.duration,
            concurrency: test.concurrency,
            targetRate:  test.targetRate,
        }

        lt.Run()

        // Cool down between tests
        time.Sleep(5 * time.Second)
    }
}
```

### Profiling Integration

```go
// Enable profiling in production
import _ "net/http/pprof"

func init() {
    go func() {
        http.ListenAndServe("localhost:6060", nil)
    }()
}

// CPU profiling
// go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30

// Memory profiling
// go tool pprof http://localhost:6060/debug/pprof/heap

// Goroutine profiling
// go tool pprof http://localhost:6060/debug/pprof/goroutine

// Block profiling (for lock contention)
runtime.SetBlockProfileRate(1)
// go tool pprof http://localhost:6060/debug/pprof/block
```

### Performance Targets

| Metric | Target | Excellent | Poor |
|--------|--------|-----------|------|
| Push latency (p99) | <100ns | <50ns | >1µs |
| End-to-end latency (p99) | <100ms | <50ms | >1s |
| Throughput (single client) | >100k/sec | >500k/sec | <10k/sec |
| Memory per 10k events | <1MB | <500KB | >5MB |
| GC pause time | <10ms | <1ms | >50ms |
| UDP packet loss | <2% | <0.5% | >5% |
| Drop rate under load | <5% | <1% | >10% |

---

## Summary: Recommended Architecture

```go
// High-performance, production-ready stats client
type StatsClient struct {
    // Buffering: Lock-free ring buffer
    buffer *RingBuffer // 16K events, ~1MB

    // Batching: Adaptive batcher
    batcher *AdaptiveBatcher // 10-100 events, 100ms max age

    // Transport: UDP with packet packing
    sender *UDPPacketPacker // 1472 byte packets

    // Memory: Object pooling
    eventPool   sync.Pool
    bufferPool  sync.Pool

    // Backpressure: Multi-strategy
    dropStrategy DropStrategy // DropByPriority
    circuitBreaker *CircuitBreaker

    // Monitoring: Self-instrumentation
    metrics *InternalMetrics
    health  *HealthChecker

    // Optimization: String interning
    interner *StringInterner
}

// Configuration for different scenarios
var (
    // High throughput, low latency
    HighThroughputConfig = Config{
        BufferSize:      16384,
        BatchSize:       100,
        MaxBatchAge:     50 * time.Millisecond,
        DropStrategy:    DropByPriority,
        EnablePooling:   true,
        EnableInterning: true,
    }

    // Reliability over performance
    ReliableConfig = Config{
        BufferSize:      32768,
        BatchSize:       20,
        MaxBatchAge:     200 * time.Millisecond,
        DropStrategy:    DropOldest,
        EnableCircuitBreaker: true,
        EnableRedundantSend:  true,
    }

    // Memory constrained
    LowMemoryConfig = Config{
        BufferSize:      4096,
        BatchSize:       50,
        MaxBatchAge:     100 * time.Millisecond,
        DropStrategy:    DropSampled,
        SampleRate:      0.5,
        EnablePooling:   true,
    }
)
```

---

## Quick Reference: Performance Checklist

- [ ] Use lock-free ring buffer for high throughput (>100k/sec)
- [ ] Implement adaptive batching (10-100 events, 100ms max)
- [ ] Pack UDP packets efficiently (stay under 1472 bytes)
- [ ] Enable object pooling for zero-allocation hot path
- [ ] Intern common strings to reduce memory footprint
- [ ] Implement priority-based backpressure handling
- [ ] Add circuit breaker for unreliable networks
- [ ] Use binary serialization (Protocol Buffers or custom)
- [ ] Monitor internal metrics (buffer utilization, drop rate, latency)
- [ ] Accept <2% UDP packet loss for non-critical metrics
- [ ] Benchmark with 3x expected peak load
- [ ] Profile CPU and memory usage under load
- [ ] Validate GC pause times remain <10ms
- [ ] Test backpressure scenarios (buffer full, network down)
- [ ] Verify graceful degradation under extreme load

---

*Performance Engineering Guidelines v1.0*
*Target: >100k events/sec, <100ms p99 latency, <1% drop rate*
