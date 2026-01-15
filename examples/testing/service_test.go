package testing_example

import (
	"context"
	"testing"

	"github.com/convoy-road-trips-app/stats"
)

func TestService(t *testing.T) {
	// In tests, we use the NoOpClient which implements the stats.Recorder interface
	// but doesn't actually do anything. This allows us to test our business logic
	// without worrying about a real stats client being initialized or sending data.
	noopStats := stats.NewNoOpClient()

	svc := NewService(noopStats)
	ctx := context.Background()

	t.Run("ProcessItems", func(t *testing.T) {
		err := svc.ProcessItems(ctx, 10)
		if err != nil {
			t.Fatalf("ProcessItems failed: %v", err)
		}
	})
}
