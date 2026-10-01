package transport

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// releaseStalledRead finishes a read claimed on slot pos without releasing it.
func releaseStalledRead(rb *RingBuffer, pos uint64) any {
	s := &rb.slots[pos&rb.mask]
	item := s.item
	s.item = nil
	s.seq.Store(pos + rb.capacity)
	return item
}

func TestRingBuffer_PushDropOldest_keeps_the_queue_when_a_stalled_consumer_holds_the_write_slot(t *testing.T) {
	// Given: a cap-2 ring whose slot 0 a consumer claimed but has not released,
	// and whose slot 1 holds a published item
	rb := NewRingBuffer(2)
	require.True(t, rb.Push("a"))
	require.True(t, rb.Push("b"))
	require.True(t, rb.readPos.CompareAndSwap(0, 1))

	// When
	evicted, ok := rb.PushDropOldest("incoming")

	// Then: only the incoming item is rejected; "b" is still delivered
	assert.False(t, ok)
	assert.Nil(t, evicted)
	assert.Equal(t, uint64(1), rb.Dropped())
	assert.Equal(t, "a", releaseStalledRead(rb, 0))
	assert.Equal(t, []any{"b"}, rb.PopBatch(4))
}

func TestRingBuffer_PushDropOldest_keeps_the_queue_when_the_oldest_item_is_not_published(t *testing.T) {
	// Given: a full cap-2 ring whose oldest slot a stalled producer claimed but has not published
	rb := NewRingBuffer(2)
	require.True(t, rb.writePos.CompareAndSwap(0, 1))
	require.True(t, rb.Push("b"))

	// When
	evicted, ok := rb.PushDropOldest("incoming")

	// Then
	assert.False(t, ok)
	assert.Nil(t, evicted)
	rb.slots[0].item = "a"
	rb.slots[0].seq.Store(1)
	assert.Equal(t, []any{"a", "b"}, rb.PopBatch(4))
}

func TestRingBuffer_PushDropOldest_replaces_the_oldest_item_of_a_full_ring(t *testing.T) {
	// Given
	rb := NewRingBuffer(2)
	require.True(t, rb.Push("a"))
	require.True(t, rb.Push("b"))

	// When
	evicted, ok := rb.PushDropOldest("c")

	// Then
	assert.True(t, ok)
	assert.Equal(t, "a", evicted)
	assert.Equal(t, [3]uint64{3, 1, 1}, [3]uint64{rb.Added(), rb.Removed(), rb.Dropped()})
	assert.Equal(t, []any{"b", "c"}, rb.PopBatch(4))
	assert.True(t, rb.Push("d"))
}

func TestRingBuffer_PushDropOldest_pushes_without_eviction_below_capacity(t *testing.T) {
	// Given
	rb := NewRingBuffer(2)
	require.True(t, rb.Push("a"))

	// When
	evicted, ok := rb.PushDropOldest("b")

	// Then
	assert.True(t, ok)
	assert.Nil(t, evicted)
	assert.Equal(t, uint64(0), rb.Dropped())
	assert.Equal(t, []any{"a", "b"}, rb.PopBatch(4))
}

func TestRingBuffer_PushDropOldest_rejects_nil(t *testing.T) {
	// Given
	rb := NewRingBuffer(2)

	// When
	evicted, ok := rb.PushDropOldest(nil)

	// Then
	assert.False(t, ok)
	assert.Nil(t, evicted)
	assert.Equal(t, 0, rb.Len())
}

func TestRingBuffer_PushDropOldest_accepted_items_are_delivered_or_evicted_exactly_once(t *testing.T) {
	for _, capacity := range []int{2, 8} {
		t.Run(fmt.Sprintf("capacity %d", capacity), func(t *testing.T) {
			assertDropOldestExactlyOnce(t, capacity)
		})
	}
}

func assertDropOldestExactlyOnce(t *testing.T, capacity int) {
	t.Helper()
	// Given: a tiny ring that drop-oldest producers overflow while consumers pop
	const producers, perProducer = 4, 20_000
	rb := NewRingBuffer(capacity)
	delivered := make([]atomic.Int32, producers*perProducer)
	evicted := make([]atomic.Int32, producers*perProducer)
	accepted := make([]atomic.Bool, producers*perProducer)
	stop := make(chan struct{})
	var consumers sync.WaitGroup
	for c := range 2 {
		consumers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					pop := rb.Pop
					if c == 1 {
						pop = rb.TryPop
					}
					item := pop()
					if item != nil {
						delivered[item.(int)].Add(1)
					}
				}
			}
		})
	}

	// When
	var producersDone sync.WaitGroup
	for p := range producers {
		producersDone.Go(func() {
			for i := range perProducer {
				id := p*perProducer + i
				old, ok := rb.PushDropOldest(id)
				accepted[id].Store(ok)
				if old != nil {
					evicted[old.(int)].Add(1)
				}
			}
		})
	}
	producersDone.Wait()
	close(stop)
	consumers.Wait()
	for item := rb.Pop(); item != nil; item = rb.Pop() {
		delivered[item.(int)].Add(1)
	}

	// Then: an accepted item is delivered xor evicted, once; a rejected one is neither
	outcomes := map[string]int{}
	for id := range delivered {
		d, e := delivered[id].Load(), evicted[id].Load()
		switch {
		case accepted[id].Load() && d+e == 1:
			outcomes["accepted once"]++
		case !accepted[id].Load() && d+e == 0:
			outcomes["rejected untouched"]++
		default:
			outcomes["violation"]++
		}
	}
	assert.Zero(t, outcomes["violation"], "%v", outcomes)
	assert.Equal(t, uint64(outcomes["rejected untouched"])+evictedTotal(evicted), rb.Dropped())
}

func evictedTotal(evicted []atomic.Int32) uint64 {
	var total uint64
	for i := range evicted {
		total += uint64(evicted[i].Load())
	}
	return total
}
