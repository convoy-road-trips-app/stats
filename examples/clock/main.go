// Command clock shows how to time the steps of an operation with stats.Clock.
// A clock records each step as one histogram observation in seconds (the unit
// Prometheus and OpenTelemetry use), with a constant "stamp" attribute naming
// the step, plus a "total" observation from Stop. It builds on Client.Observe;
// compare Timing, which records milliseconds.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/convoy-road-trips-app/stats"
	"github.com/convoy-road-trips-app/stats/debugstats"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	client, err := stats.NewClient(
		stats.WithServiceName("clock-example"),
		stats.WithVersionReporting(false),
		stats.WithFlushInterval(time.Hour),
		stats.WithExporter(&debugstats.Exporter{Dst: os.Stdout}),
	)
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	// A clock measures one sequence of steps and starts when it is created.
	// Stamp records the time since the previous Stamp (or the start) under the
	// given step name; Stop records the whole sequence as stamp "total".
	// Use constant step names: every distinct name is a new series.
	clock := client.Clock("job.duration", stats.WithAttribute("job", "nightly"))
	steps := []struct {
		name string
		work time.Duration
	}{
		{"load", 10 * time.Millisecond},
		{"transform", 20 * time.Millisecond},
		{"store", 5 * time.Millisecond},
	}
	for _, step := range steps {
		time.Sleep(step.work)
		if err := clock.Stamp(ctx, step.name); err != nil {
			return fmt.Errorf("stamp: %w", err)
		}
	}
	if err := clock.Stop(ctx); err != nil {
		return fmt.Errorf("stop: %w", err)
	}

	// For a single duration, Observe is on the optional stats.DurationObserver
	// interface, so code that holds only a stats.Recorder can use it through a
	// type assertion.
	var rec stats.Recorder = client
	if o, ok := rec.(stats.DurationObserver); ok {
		_ = o.Observe(ctx, "cleanup.duration", 3*time.Millisecond)
	}

	flushCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := client.Flush(flushCtx); err != nil {
		return fmt.Errorf("flush: %w", err)
	}
	return nil
}
