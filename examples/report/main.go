// Command report shows stats.Report: struct tags describe the metrics, and one
// call records them all.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/convoy-road-trips-app/stats"
	"github.com/convoy-road-trips-app/stats/debugstats"
)

// RequestStats is reported with Report. A tag:"key" string field becomes an
// attribute on every metric of its struct (and nested structs); a metric:"name"
// field is a value, or a name prefix on a nested struct.
type RequestStats struct {
	Route string `tag:"route"`

	Count    int           `metric:"requests" type:"counter"`
	Latency  time.Duration `metric:"latency"` // histogram by default; reported in seconds
	InFlight int           `metric:"in_flight" type:"gauge"`
	Failed   bool          `metric:"failed" type:"counter"` // bool is 0 or 1

	Cache struct {
		Hits   int `metric:"hits" type:"counter"`
		Misses int `metric:"misses" type:"counter"`
	} `metric:"cache"` // names become cache.hits and cache.misses
}

func main() {
	client, err := stats.NewClient(
		stats.WithServiceName("report-example"),
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

	m := RequestStats{Route: "/users/{id}", Count: 1, Latency: 125 * time.Millisecond, InFlight: 3}
	m.Cache.Hits = 7
	m.Cache.Misses = 2

	// One struct...
	if err := stats.Report(ctx, client, &m); err != nil {
		fmt.Fprintln(os.Stderr, "report:", err)
		os.Exit(1)
	}

	// ...or a slice of them, with extra options applied to every metric.
	batch := []RequestStats{{Route: "/health", Count: 1}, {Route: "/orders", Count: 4, Failed: true}}
	if err := stats.Report(ctx, client, batch, stats.WithAttribute("source", "batch")); err != nil {
		fmt.Fprintln(os.Stderr, "report batch:", err)
		os.Exit(1)
	}

	flushCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := client.Flush(flushCtx); err != nil {
		fmt.Fprintln(os.Stderr, "flush:", err)
		os.Exit(1)
	}
}
