// Command netstats shows connection instrumentation: a listener wrapped with
// netstats.NewListenerWith counts the connections it accepts, and a dialed
// connection is wrapped with netstats.NewConnWith. netstats batches read and
// write counts per connection and flushes them when the connection closes.
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"time"

	"github.com/convoy-road-trips-app/stats"
	"github.com/convoy-road-trips-app/stats/debugstats"
	"github.com/convoy-road-trips-app/stats/netstats"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	client, err := stats.NewClient(
		stats.WithServiceName("netstats-example"),
		stats.WithVersionReporting(false),
		stats.WithFlushInterval(time.Hour),
		// Print only the byte histograms and close counters.
		stats.WithExporter(&debugstats.Exporter{
			Dst:  os.Stdout,
			Grep: regexp.MustCompile(`^conn\.(read|write)\.bytes|^conn\.close\.count`),
		}),
	)
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var lc net.ListenConfig
	raw, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	zones := netstats.WithZones("us-east-1a", "us-east-1a")
	ln := netstats.NewListenerWith(client, raw, zones)
	defer func() { _ = ln.Close() }()

	// Echo server: every accepted connection is already instrumented.
	served := make(chan struct{})
	go func() {
		defer close(served)
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		_, _ = io.Copy(c, c)
	}()

	// Client side: wrap the dialed connection.
	dialer := net.Dialer{Timeout: 2 * time.Second}
	nc, err := dialer.DialContext(ctx, "tcp", raw.Addr().String())
	if err != nil {
		return err
	}
	conn := netstats.NewConnWith(client, nc, zones)
	if _, err := conn.Write([]byte("ping")); err != nil {
		return err
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return err
	}
	fmt.Printf("echoed %q\n", buf)

	// Closing flushes the batched totals. Closing the client end makes the
	// server's io.Copy return and close its end too.
	if err := conn.Close(); err != nil {
		return err
	}
	<-served

	return client.Flush(ctx)
}
