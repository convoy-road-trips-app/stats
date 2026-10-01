package transport

import (
	"runtime"
	"sync/atomic"
)

// RingBuffer is a bounded, multi-producer multi-consumer ring buffer
// Optimized for high-throughput metric collection with minimal allocation.
//
// Each slot carries a sequence number (Vyukov's bounded MPMC queue): a slot is
// free for the writer at position pos when seq == pos, and holds that
// writer's item when seq == pos+1. A reader releases it for the next lap by
// storing pos+capacity. Claiming a position therefore never reuses a slot
// whose previous item has not been read yet.
type RingBuffer struct {
	slots    []slot
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

type slot struct {
	seq  atomic.Uint64
	item any // published and released through seq
}

// NewRingBuffer creates a new ring buffer with the specified capacity
// Capacity must be a power of 2 for optimal performance
func NewRingBuffer(capacity int) *RingBuffer {
	// Round up to next power of 2
	capCount := nextPowerOfTwo(uint64(capacity))

	slots := make([]slot, capCount)
	for i := range slots {
		slots[i].seq.Store(uint64(i))
	}
	return &RingBuffer{
		slots:    slots,
		capacity: capCount,
		mask:     capCount - 1,
	}
}

// Push adds an item to the buffer. Returns false if buffer is full (non-blocking)
func (rb *RingBuffer) Push(item any) bool {
	if item == nil {
		return false
	}

	for {
		// Load readPos first: readers never pass writePos, so a later writePos
		// load is >= readPos and writePos-readPos cannot underflow into "full".
		readPos := rb.readPos.Load()
		writePos := rb.writePos.Load()

		// Check if buffer is full
		if writePos-readPos >= rb.capacity {
			rb.dropped.Add(1)
			return false
		}

		s := &rb.slots[writePos&rb.mask]
		switch seq := s.seq.Load(); {
		case seq < writePos:
			// The slot still holds the previous lap's item: its reader claimed
			// it but has not released it yet. Report full instead of waiting
			// on that reader, so Push never depends on a consumer running.
			rb.dropped.Add(1)
			return false
		case seq == writePos && rb.writePos.CompareAndSwap(writePos, writePos+1):
			s.item = item
			s.seq.Store(writePos + 1)
			rb.added.Add(1)
			return true
		}
		// Stale writePos or lost CAS: retry
	}
}

// Pop removes and returns an item from the buffer. Returns nil if buffer is
// empty. If the next item's writer has claimed its slot but not stored the
// item yet, Pop waits for it, so items are popped in claim order.
func (rb *RingBuffer) Pop() any {
	return rb.pop(true)
}

// TryPop is Pop without the wait: it also returns nil when the next item's
// writer has claimed its slot but not stored the item yet, and leaves that
// slot to the writer. Use it where the caller must never depend on another
// goroutine running.
func (rb *RingBuffer) TryPop() any {
	return rb.pop(false)
}

func (rb *RingBuffer) pop(waitForWriter bool) any {
	for {
		readPos := rb.readPos.Load()
		if readPos >= rb.writePos.Load() {
			return nil
		}

		s := &rb.slots[readPos&rb.mask]
		switch seq := s.seq.Load(); {
		case seq <= readPos:
			// The writer claimed the slot but has not stored the item yet.
			if !waitForWriter {
				return nil
			}
			runtime.Gosched()
		case seq == readPos+1 && rb.readPos.CompareAndSwap(readPos, readPos+1):
			item := s.item
			s.item = nil // Clear the slot for GC
			s.seq.Store(readPos + rb.capacity)
			rb.removed.Add(1)
			return item
		}
		// Stale readPos or lost CAS: retry
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
