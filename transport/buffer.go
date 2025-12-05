package transport

import (
	"sync/atomic"
)

// RingBuffer is a lock-free, bounded, single-producer-multiple-consumer ring buffer
// Optimized for high-throughput metric collection with minimal allocation
type RingBuffer struct {
	buffer   []any
	capacity uint64
	mask     uint64

	// Use separate cache lines to avoid false sharing
	_padding0 [64]byte
	writePos  atomic.Uint64
	_padding1 [64]byte
	readPos   atomic.Uint64
	_padding2 [64]byte

	// Metrics
	dropped atomic.Uint64
	added   atomic.Uint64
	removed atomic.Uint64
}

// NewRingBuffer creates a new ring buffer with the specified capacity
// Capacity must be a power of 2 for optimal performance
func NewRingBuffer(capacity int) *RingBuffer {
	// Round up to next power of 2
	cap := nextPowerOfTwo(uint64(capacity))

	return &RingBuffer{
		buffer:   make([]any, cap),
		capacity: cap,
		mask:     cap - 1,
	}
}

// Push adds an item to the buffer. Returns false if buffer is full (non-blocking)
func (rb *RingBuffer) Push(item any) bool {
	if item == nil {
		return false
	}

	for {
		writePos := rb.writePos.Load()
		readPos := rb.readPos.Load()

		// Check if buffer is full
		if writePos-readPos >= rb.capacity {
			rb.dropped.Add(1)
			return false
		}

		// Try to claim this slot
		if rb.writePos.CompareAndSwap(writePos, writePos+1) {
			// Successfully claimed slot, write the item
			idx := writePos & rb.mask
			rb.buffer[idx] = item
			rb.added.Add(1)
			return true
		}
		// CAS failed, retry
	}
}

// Pop removes and returns an item from the buffer. Returns nil if buffer is empty
func (rb *RingBuffer) Pop() any {
	for {
		readPos := rb.readPos.Load()
		writePos := rb.writePos.Load()

		// Check if buffer is empty
		if readPos >= writePos {
			return nil
		}

		// Try to claim this slot
		if rb.readPos.CompareAndSwap(readPos, readPos+1) {
			// Successfully claimed slot, read the item
			idx := readPos & rb.mask
			item := rb.buffer[idx]
			rb.buffer[idx] = nil // Help GC
			rb.removed.Add(1)
			return item
		}
		// CAS failed, retry
	}
}

// PopBatch attempts to pop up to maxItems from the buffer
// Returns a slice of items (may be less than maxItems if buffer has fewer)
func (rb *RingBuffer) PopBatch(maxItems int) []any {
	if maxItems <= 0 {
		return nil
	}

	batch := make([]any, 0, maxItems)

	for i := 0; i < maxItems; i++ {
		item := rb.Pop()
		if item == nil {
			break
		}
		batch = append(batch, item)
	}

	return batch
}

// Len returns the approximate number of items in the buffer
func (rb *RingBuffer) Len() int {
	writePos := rb.writePos.Load()
	readPos := rb.readPos.Load()
	if writePos < readPos {
		return 0
	}
	return int(writePos - readPos)
}

// Cap returns the capacity of the buffer
func (rb *RingBuffer) Cap() int {
	return int(rb.capacity)
}

// Dropped returns the number of items dropped due to buffer full
func (rb *RingBuffer) Dropped() uint64 {
	return rb.dropped.Load()
}

// Added returns the total number of items added to the buffer
func (rb *RingBuffer) Added() uint64 {
	return rb.added.Load()
}

// Removed returns the total number of items removed from the buffer
func (rb *RingBuffer) Removed() uint64 {
	return rb.removed.Load()
}

// IsFull returns true if the buffer is full
func (rb *RingBuffer) IsFull() bool {
	writePos := rb.writePos.Load()
	readPos := rb.readPos.Load()
	return writePos-readPos >= rb.capacity
}

// IsEmpty returns true if the buffer is empty
func (rb *RingBuffer) IsEmpty() bool {
	return rb.Len() == 0
}

// ResetStats resets all statistics counters
func (rb *RingBuffer) ResetStats() {
	rb.dropped.Store(0)
	rb.added.Store(0)
	rb.removed.Store(0)
}

// nextPowerOfTwo returns the next power of 2 greater than or equal to n
func nextPowerOfTwo(n uint64) uint64 {
	if n == 0 {
		return 1
	}
	n--
	n |= n >> 1
	n |= n >> 2
	n |= n >> 4
	n |= n >> 8
	n |= n >> 16
	n |= n >> 32
	n++
	return n
}
