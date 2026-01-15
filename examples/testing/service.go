package testing_example

import (
	"context"
	"fmt"

	"github.com/convoy-road-trips-app/stats"
)

// Service is an example service that uses the stats library.
type Service struct {
	stats stats.Recorder
}

// NewService creates a new Service with the given stats recorder.
func NewService(s stats.Recorder) *Service {
	return &Service{
		stats: s,
	}
}

// ProcessItems simulates processing some items and recording metrics.
func (s *Service) ProcessItems(ctx context.Context, count int) error {
	// Increment a counter
	if err := s.stats.Increment(ctx, "items.processed", stats.WithAttribute("count", fmt.Sprintf("%d", count))); err != nil {
		return err
	}

	// Record a gauge
	if err := s.stats.Gauge(ctx, "items.batch_size", float64(count)); err != nil {
		return err
	}

	return nil
}
