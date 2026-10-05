package stats

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestClientRecord_concurrent_recording_during_shutdown_neither_races_nor_deadlocks(t *testing.T) {
	// Given: a client with 100 goroutines recording through every recording method
	client, err := NewClient(WithServiceName("record-shutdown"))
	require.NoError(t, err)

	const goroutines = 100
	var started, finished sync.WaitGroup
	started.Add(goroutines)
	finished.Add(goroutines)
	stop := make(chan struct{})
	for i := range goroutines {
		go func() {
			defer finished.Done()
			started.Done()
			ctx := context.Background()
			for {
				select {
				case <-stop:
					return
				default:
				}
				switch i % 4 {
				case 0:
					_ = client.Counter(ctx, "c", 1, WithAttribute("k", "v"))
				case 1:
					_ = client.Gauge(ctx, "g", 1)
				case 2:
					_ = client.Histogram(ctx, "h", 1)
				default:
					_ = client.RecordMetric(ctx, NewCounter("r", 1).Build())
				}
			}
		}()
	}
	started.Wait()

	// When: the client shuts down while they record
	done := make(chan error, 1)
	go func() { done <- client.Shutdown(context.Background()) }()

	// Then: shutdown and every recorder finish within 5s, and recording reports ErrClientClosed
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not return within 5s")
	}
	require.ErrorIs(t, client.Counter(context.Background(), "after", 1), ErrClientClosed)
	close(stop)

	finishedCh := make(chan struct{})
	go func() { finished.Wait(); close(finishedCh) }()
	select {
	case <-finishedCh:
	case <-time.After(5 * time.Second):
		t.Fatal("recording goroutines did not stop within 5s")
	}
}
