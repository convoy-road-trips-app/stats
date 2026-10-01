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
	// Round up to next power of 2, and to at least 2: with one slot the
	// published value (pos+1) equals the released value (pos+capacity), so a
	// writer could overwrite an item its reader has claimed but not read.
	capCount := max(nextPowerOfTwo(uint64(max(capacity, 0))), 2)

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

// attempt is the outcome of one lock-free step of a push.
type attempt int

const (
	retry    attempt = iota // stale position or lost CAS
	pushed                  // the item was published
	rejected                // the item cannot be published without waiting
)

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

		result := rejected // the buffer is full
		if writePos-readPos < rb.capacity {
			result = rb.pushAt(writePos, item)
		}
		switch result {
		case pushed:
			return true
		case rejected:
			rb.dropped.Add(1)
			return false
		case retry:
			// Stale position or lost CAS: loop again
		}
	}
}

// PushDropOldest adds an item to the buffer, evicting the oldest item when
// the buffer is full, and never waits on another goroutine. The eviction and
// the push are one step: an item is evicted only if item takes its place, and
// the evicted item is returned so the caller can release it.
//
// It returns ok=false, and leaves the buffered items untouched, when item is
// nil or when publishing it would mean waiting: the write slot is still held
// by a reader that has not released it, or the oldest item's writer has not
// published it yet.
func (rb *RingBuffer) PushDropOldest(item any) (evicted any, ok bool) {
	if item == nil {
		return nil, false
	}

	for {
		readPos := rb.readPos.Load()
		writePos := rb.writePos.Load()

		var result attempt
		if writePos-readPos < rb.capacity {
			result = rb.pushAt(writePos, item)
		} else {
			result, evicted = rb.replaceOldest(readPos, writePos, item)
		}
		switch result {
		case pushed:
			return evicted, true
		case rejected:
			rb.dropped.Add(1)
			return nil, false
		case retry:
			// Stale position or lost CAS: loop again
		}
	}
}

// pushAt publishes item at writePos of a buffer that is not full.
func (rb *RingBuffer) pushAt(writePos uint64, item any) attempt {
	s := &rb.slots[writePos&rb.mask]
	switch seq := s.seq.Load(); {
	case seq < writePos:
		// The slot still holds the previous lap's item: its reader claimed
		// it but has not released it yet. Report full instead of waiting
		// on that reader, so Push never depends on a consumer running.
		return rejected
	case seq == writePos && rb.writePos.CompareAndSwap(writePos, writePos+1):
		s.item = item
		s.seq.Store(writePos + 1)
		rb.added.Add(1)
		return pushed
	}
	return retry
}

// replaceOldest evicts the item at readPos of a full buffer and publishes item
// at writePos. On a full buffer writePos = readPos+capacity, so both
// positions share one slot: taking the oldest item as its reader also makes
// the slot ours to write, and no other writer can claim writePos meanwhile.
func (rb *RingBuffer) replaceOldest(readPos, writePos uint64, item any) (result attempt, evicted any) {
	s := &rb.slots[readPos&rb.mask]
	switch seq := s.seq.Load(); {
	case seq <= readPos:
		// The oldest item's writer has not published it yet.
		return rejected, nil
	case seq != readPos+1 || writePos != readPos+rb.capacity:
		return retry, nil
	}

	// Mark the slot unpublished before claiming readPos, so that readers wait
	// (or TryPop reports empty) and writers report full until item is
	// published.
	if !s.seq.CompareAndSwap(readPos+1, readPos) {
		return retry, nil
	}
	if !rb.readPos.CompareAndSwap(readPos, readPos+1) {
		// A reader that saw the published item claimed it first and will
		// release the slot itself, which overwrites the mark.
		return retry, nil
	}

	old := s.item
	rb.writePos.Store(writePos + 1) // nobody else can claim it, see above
	s.item = item
	s.seq.Store(writePos + 1)
	rb.removed.Add(1)
	rb.dropped.Add(1)
	rb.added.Add(1)
	return pushed, old
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
