// Command clock shows how to time the steps of an operation with Observe, which
// records a time.Duration as a histogram in seconds (the unit Prometheus and
// OpenTelemetry use). Compare Timing, which records milliseconds.
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
	client, err := stats.NewClient(
		stats.WithServiceName("clock-example"),
		stats.WithVersionReporting(false),
		stats.WithFlushInterval(time.Hour),
		stats.WithExporter(&debugstats.Exporter{Dst: os.Stdout}),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "create client:", err)
		os.Exit(1)
	}
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	// Time each step of a sequence, naming it with a constant "stamp" attribute
	// so the steps share one histogram series family.
	begin := time.Now()
	last := begin
	step := func(name string, work time.Duration) {
		time.Sleep(work)
		now := time.Now()
		_ = client.Observe(ctx, "job.duration", now.Sub(last), stats.WithAttribute("stamp", name))
		last = now
	}
	step("load", 10*time.Millisecond)
	step("transform", 20*time.Millisecond)
	step("store", 5*time.Millisecond)
	_ = client.Observe(ctx, "job.duration", time.Since(begin), stats.WithAttribute("stamp", "total"))

	// Observe is on the optional stats.DurationObserver interface, so code that
	// holds only a stats.Recorder can use it through a type assertion.
	var rec stats.Recorder = client
	if o, ok := rec.(stats.DurationObserver); ok {
		_ = o.Observe(ctx, "cleanup.duration", 3*time.Millisecond)
	}

	flushCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := client.Flush(flushCtx); err != nil {
		fmt.Fprintln(os.Stderr, "flush:", err)
		os.Exit(1)
	}
}
