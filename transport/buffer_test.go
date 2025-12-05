package transport

import (
	"sync"
	"sync/atomic"
	"testing"

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
	rb := NewRingBuffer(1024)
	iterations := 1000

	var wg sync.WaitGroup
	var pushCount, popCount atomic.Uint64

	// Start producers
	producers := 2
	for i := 0; i < producers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				if rb.Push(id*iterations + j) {
					pushCount.Add(1)
				}
			}
		}(i)
	}

	// Start consumers
	consumers := 2
	for i := 0; i < consumers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				if item := rb.Pop(); item != nil {
					popCount.Add(1)
				}
			}
		}()
	}

	// Wait for all goroutines
	wg.Wait()

	// Drain any remaining items
	for {
		if item := rb.Pop(); item != nil {
			popCount.Add(1)
		} else {
			break
		}
	}

	// Verify counts match
	assert.Equal(t, pushCount.Load(), popCount.Load())
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
