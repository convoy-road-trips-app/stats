package stats

import (
	"context"
	"testing"
	"time"
)

func TestNoOpClient(t *testing.T) {
	var recorder Recorder = NewNoOpClient()
	ctx := context.Background()

	t.Run("Counter", func(t *testing.T) {
		if err := recorder.Counter(ctx, "test_counter", 1.0); err != nil {
			t.Errorf("Counter failed: %v", err)
		}
	})

	t.Run("Gauge", func(t *testing.T) {
		if err := recorder.Gauge(ctx, "test_gauge", 1.0); err != nil {
			t.Errorf("Gauge failed: %v", err)
		}
	})

	t.Run("Histogram", func(t *testing.T) {
		if err := recorder.Histogram(ctx, "test_histogram", 1.0); err != nil {
			t.Errorf("Histogram failed: %v", err)
		}
	})

	t.Run("Increment", func(t *testing.T) {
		if err := recorder.Increment(ctx, "test_inc"); err != nil {
			t.Errorf("Increment failed: %v", err)
		}
	})

	t.Run("Timing", func(t *testing.T) {
		if err := recorder.Timing(ctx, "test_timing", time.Second); err != nil {
			t.Errorf("Timing failed: %v", err)
		}
	})

	t.Run("RecordMetric", func(t *testing.T) {
		m := NewCounter("test", 1).Build()
		if err := recorder.RecordMetric(ctx, m); err != nil {
			t.Errorf("RecordMetric failed: %v", err)
		}
	})

	t.Run("Stats", func(t *testing.T) {
		stats := recorder.Stats()
		if stats.ServiceName != "" {
			t.Errorf("Expected empty stats, got %+v", stats)
		}
	})

	t.Run("Lifecycle", func(t *testing.T) {
		if err := recorder.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown failed: %v", err)
		}
		if err := recorder.Close(); err != nil {
			t.Errorf("Close failed: %v", err)
		}
	})
}
