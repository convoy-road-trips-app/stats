// Command debugstats shows the debugstats console exporter: every metric the
// client records is printed as one StatsD-format line, so you can see exactly
// what an application emits without running a backend.
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"regexp"
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
	// Filtered is a second exporter that only keeps lines matching Grep. It
	// writes to a buffer here; any io.Writer works.
	var filtered bytes.Buffer

	client, err := stats.NewClient(
		stats.WithServiceName("debugstats-example"),
		stats.WithVersionReporting(false),
		stats.WithFlushInterval(time.Hour), // flush explicitly below
		// Unfiltered: a nil Dst writes to os.Stdout.
		stats.WithExporter(&debugstats.Exporter{Dst: os.Stdout}),
		// A second exporter needs its own name, so wrap it (see named below).
		stats.WithExporter(named{
			name: "debugstats-errors",
			Exporter: &debugstats.Exporter{
				Dst:  &filtered,
				Grep: regexp.MustCompile(`^error\.`),
			},
		}),
	)
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	fmt.Println("--- every metric, as it is exported ---")
	_ = client.Counter(ctx, "server.start", 1)
	_ = client.Counter(ctx, "http.requests", 1, stats.WithAttribute("method", "GET"))
	_ = client.Gauge(ctx, "queue.depth", 12)
	_ = client.Counter(ctx, "error.count", 1, stats.WithAttribute("kind", "timeout"))

	flushCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := client.Flush(flushCtx); err != nil {
		return fmt.Errorf("flush: %w", err)
	}

	fmt.Println("--- only lines matching ^error\\. ---")
	fmt.Print(filtered.String())
	return nil
}

// named gives a debugstats.Exporter another name: the client requires every
// exporter to have a unique one, and debugstats always reports "debugstats".
type named struct {
	name string
	*debugstats.Exporter
}

func (n named) Name() string { return n.name }
