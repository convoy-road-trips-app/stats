package transport

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewRingBuffer(t *testing.T) {
	tests := []struct {
		name     string
		capacity int
		wantCap  int
	}{
		{"power of 2", 1024, 1024},
		{"non-power of 2", 1000, 1024},
		{"small", 10, 16},
		{"zero", 0, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rb := NewRingBuffer(tt.capacity)
			assert.Equal(t, tt.wantCap, rb.Cap())
			assert.Equal(t, 0, rb.Len())
			assert.True(t, rb.IsEmpty())
			assert.False(t, rb.IsFull())
		})
	}
}

func TestRingBuffer_PushPop(t *testing.T) {
	rb := NewRingBuffer(4)

	// Push items
	assert.True(t, rb.Push("item1"))
	assert.Equal(t, 1, rb.Len())

	assert.True(t, rb.Push("item2"))
	assert.Equal(t, 2, rb.Len())

	// Pop items
	item := rb.Pop()
	assert.Equal(t, "item1", item)
	assert.Equal(t, 1, rb.Len())

	item = rb.Pop()
	assert.Equal(t, "item2", item)
	assert.Equal(t, 0, rb.Len())
	assert.True(t, rb.IsEmpty())

	// Pop from empty
	item = rb.Pop()
	assert.Nil(t, item)
}

func TestRingBuffer_Full(t *testing.T) {
	rb := NewRingBuffer(4)

	// Fill buffer
	assert.True(t, rb.Push("item1"))
	assert.True(t, rb.Push("item2"))
	assert.True(t, rb.Push("item3"))
	assert.True(t, rb.Push("item4"))
	assert.True(t, rb.IsFull())

	// Try to push when full
	assert.False(t, rb.Push("item5"))
	assert.Equal(t, uint64(1), rb.Dropped())

	// Pop one and try again
	rb.Pop()
	assert.False(t, rb.IsFull())
	assert.True(t, rb.Push("item5"))
}

func TestRingBuffer_PopBatch(t *testing.T) {
	rb := NewRingBuffer(16)

	// Push items
	for i := 0; i < 10; i++ {
		rb.Push(i)
	}

	// Pop batch
	batch := rb.PopBatch(5)
	assert.Len(t, batch, 5)
	for i := 0; i < 5; i++ {
		assert.Equal(t, i, batch[i])
	}
	assert.Equal(t, 5, rb.Len())

	// Pop remaining
	batch = rb.PopBatch(10)
	assert.Len(t, batch, 5) // Only 5 left
	assert.True(t, rb.IsEmpty())

	// Pop from empty
	batch = rb.PopBatch(5)
	assert.Len(t, batch, 0)
}

func TestRingBuffer_Concurrent(t *testing.T) {
	producers := 2
	consumers := 2
	iterations := 1000
	totalPushAttempts := uint64(producers * iterations)

	rb := NewRingBuffer(4096)

	var wg sync.WaitGroup

	for i := 0; i < producers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				rb.Push(id*iterations + j)
			}
		}(i)
	}

	for i := 0; i < consumers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				rb.Pop()
			}
		}()
	}

	wg.Wait()

	for rb.Pop() != nil {
	}

	assert.Equal(t, totalPushAttempts, rb.Added()+rb.Dropped())
	assert.LessOrEqual(t, rb.Dropped(), uint64(producers))
	assert.Equal(t, rb.Added(), rb.Removed())
	assert.Equal(t, 0, rb.Len())
}

func TestRingBuffer_Stats(t *testing.T) {
	rb := NewRingBuffer(4)

	// Add some items
	rb.Push("item1")
	rb.Push("item2")
	assert.Equal(t, uint64(2), rb.Added())

	// Remove some items
	rb.Pop()
	assert.Equal(t, uint64(1), rb.Removed())

	// Fill the buffer
	rb.Push("item3")
	rb.Push("item4")
	rb.Push("item5")
	// Now buffer is full (has 4 items)

	// This should be dropped
	rb.Push("item6")
	assert.Equal(t, uint64(1), rb.Dropped())

	// Reset stats
	rb.ResetStats()
	assert.Equal(t, uint64(0), rb.Added())
	assert.Equal(t, uint64(0), rb.Removed())
	assert.Equal(t, uint64(0), rb.Dropped())
}

func BenchmarkRingBuffer_Push(b *testing.B) {
	rb := NewRingBuffer(16384)
	item := "test"

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			rb.Push(item)
		}
	})
}

func BenchmarkRingBuffer_Pop(b *testing.B) {
	rb := NewRingBuffer(16384)

	// Pre-fill buffer
	for i := 0; i < 10000; i++ {
		rb.Push(i)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			rb.Pop()
		}
	})
}

func BenchmarkRingBuffer_PushPop(b *testing.B) {
	rb := NewRingBuffer(16384)
	item := "test"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rb.Push(item)
		rb.Pop()
	}
}

func TestRingBuffer_Push_never_reports_full_below_capacity_while_consumers_pop(t *testing.T) {
	// Given: far fewer items than capacity, pushed and popped concurrently
	const producers, perProducer = 8, 20_000
	rb := NewRingBuffer(producers * perProducer * 2)
	stop := make(chan struct{})
	var consumers sync.WaitGroup
	for range 4 {
		consumers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					rb.Pop()
				}
			}
		})
	}

	// When
	var producersDone sync.WaitGroup
	for range producers {
		producersDone.Go(func() {
			for i := range perProducer {
				rb.Push(i)
			}
		})
	}
	producersDone.Wait()
	close(stop)
	consumers.Wait()

	// Then: a stale write position must not be mistaken for a full buffer
	assert.Equal(t, uint64(0), rb.Dropped())
}

func TestRingBuffer_delivers_every_pushed_item_exactly_once_while_the_buffer_is_full(t *testing.T) {
	// Given: a tiny ring that producers keep at capacity while consumers pop
	const producers, perProducer = 4, 20_000
	rb := NewRingBuffer(8)
	deliveries := make([]atomic.Int32, producers*perProducer)
	take := func(item any) { deliveries[item.(int)].Add(1) }
	stop := make(chan struct{})
	var consumers sync.WaitGroup
	for range 2 {
		consumers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					for _, item := range rb.PopBatch(3) {
						take(item)
					}
				}
			}
		})
	}

	// When: every item is pushed, retrying while the ring reports full
	var producersDone sync.WaitGroup
	for p := range producers {
		producersDone.Go(func() {
			for i := range perProducer {
				for !rb.Push(p*perProducer + i) {
					runtime.Gosched()
				}
			}
		})
	}
	producersDone.Wait()
	close(stop)
	consumers.Wait()
	for item := rb.Pop(); item != nil; item = rb.Pop() {
		take(item)
	}

	// Then: no pushed item is lost or popped twice
	timesDelivered := map[int32]int{}
	for i := range deliveries {
		timesDelivered[deliveries[i].Load()]++
	}
	assert.Equal(t, map[int32]int{1: producers * perProducer}, timesDelivered)
}

func TestRingBuffer_Push_reports_full_instead_of_waiting_for_a_stalled_consumer(t *testing.T) {
	// Given: a full ring whose next slot a consumer has claimed but, stalled
	// between claim and release, still holds
	rb := NewRingBuffer(2)
	assert.True(t, rb.Push("first"))
	assert.True(t, rb.Push("second"))
	assert.True(t, rb.readPos.CompareAndSwap(0, 1))

	// When
	pushed := make(chan bool, 1)
	go func() { pushed <- rb.Push("third") }()

	// Then: Push returns at once and counts the drop
	select {
	case ok := <-pushed:
		assert.False(t, ok)
		assert.Equal(t, uint64(1), rb.Dropped())
	case <-time.After(5 * time.Second):
		t.Fatal("Push blocked on a consumer that had not released its slot")
	}
}

func TestRingBuffer_Push_reuses_a_slot_once_its_consumer_releases_it(t *testing.T) {
	// Given: a full ring whose oldest item was popped
	rb := NewRingBuffer(2)
	assert.True(t, rb.Push("first"))
	assert.True(t, rb.Push("second"))
	assert.Equal(t, "first", rb.Pop())

	// When
	ok := rb.Push("third")

	// Then
	assert.True(t, ok)
	assert.Equal(t, []any{"second", "third"}, rb.PopBatch(3))
}

func TestRingBuffer_TryPop_reports_empty_instead_of_waiting_for_a_stalled_producer(t *testing.T) {
	// Given: a producer claimed the next slot but, stalled between claim and
	// publish, has not stored its item
	rb := NewRingBuffer(2)
	assert.True(t, rb.writePos.CompareAndSwap(0, 1))

	// When
	popped := make(chan any, 1)
	go func() { popped <- rb.TryPop() }()

	// Then: TryPop returns at once and leaves the slot to its producer
	select {
	case item := <-popped:
		assert.Nil(t, item)
		assert.Equal(t, uint64(0), rb.Removed())
	case <-time.After(5 * time.Second):
		t.Fatal("TryPop blocked on a producer that had not published its item")
	}
}

func TestRingBuffer_TryPop_removes_the_oldest_published_item(t *testing.T) {
	// Given
	rb := NewRingBuffer(2)
	assert.True(t, rb.Push("first"))
	assert.True(t, rb.Push("second"))

	// When
	item := rb.TryPop()

	// Then
	assert.Equal(t, "first", item)
	assert.True(t, rb.Push("third"))
	assert.Equal(t, []any{"second", "third"}, rb.PopBatch(3))
}
