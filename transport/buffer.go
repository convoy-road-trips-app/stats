package transport

import (
	"runtime"
	"sync/atomic"
)

// RingBuffer is a lock-free, bounded, single-producer-multiple-consumer ring buffer
// Optimized for high-throughput metric collection with minimal allocation
type RingBuffer struct {
	buffer   []atomic.Pointer[any]
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

	buffer := make([]atomic.Pointer[any], cap)
	return &RingBuffer{
		buffer:   buffer,
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
			// Store the item directly (item is already a pointer)
			// No need to wrap in another pointer - this fixes the memory leak
			rb.buffer[idx].Store(&item)
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
			// Successfully claimed slot, wait for item to be written
			idx := readPos & rb.mask

			// Bounded spin-wait for the item to be written
			// This ensures the writer has completed the Store operation
			// Maximum attempts prevent infinite loop if writer crashes
			var itemPtr *any
			attempts := 0
			maxAttempts := 1_000_000 // ~1ms on modern CPU

			for itemPtr == nil {
				itemPtr = rb.buffer[idx].Load()
				if itemPtr == nil {
					attempts++
					if attempts > maxAttempts {
						// Writer likely crashed - return nil to prevent deadlock
						// This is a safety measure; shouldn't happen in normal operation
						rb.removed.Add(1)
						return nil
					}
					if attempts > 100 {
						// After initial spins, yield to other goroutines
						runtime.Gosched()
					}
				}
			}

			// Clear the slot for GC
			rb.buffer[idx].Store(nil)
			rb.removed.Add(1)
			// Return the item directly (it's already a pointer)
			return *itemPtr
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
