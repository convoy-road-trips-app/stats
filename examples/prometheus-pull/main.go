// Command prometheus-pull shows Prometheus pull exposition: the client folds
// metrics into a prometheus.Handler, an http.Handler a Prometheus server would
// scrape. The example serves it on a random local port and scrapes itself once.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/convoy-road-trips-app/stats"
	"github.com/convoy-road-trips-app/stats/exporters/prometheus"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	// The handler is both the exporter (it stores metrics) and the scrape
	// endpoint (it renders them).
	h := &prometheus.Handler{}

	client, err := stats.NewClient(
		stats.WithServiceName("prometheus-pull-example"),
		stats.WithVersionReporting(false),
		stats.WithFlushInterval(time.Hour),
		stats.WithPrometheusHandler(h),
	)
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := record(ctx, client); err != nil {
		return err
	}

	// Serve /metrics on a free port of the loopback interface.
	mux := http.NewServeMux()
	mux.Handle("/metrics", h)
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "serve:", err)
		}
	}()
	defer func() { _ = srv.Shutdown(context.Background()) }()

	url := "http://" + ln.Addr().String() + "/metrics"
	fmt.Println("scraping", url)

	body, err := scrape(ctx, url)
	if err != nil {
		return err
	}

	// Counters are exposed with a _total suffix.
	found := false
	for line := range strings.SplitSeq(string(body), "\n") {
		if strings.Contains(line, "_total") {
			fmt.Println(line)
			found = true
		}
	}
	if !found {
		return errors.New("no _total line in scrape output")
	}
	return nil
}

// record emits a few metrics and flushes them: they reach the handler
// asynchronously, so Flush is what makes them visible to a scrape.
func record(ctx context.Context, client *stats.Client) error {
	for range 3 {
		if err := client.Counter(ctx, "http.requests", 1, stats.WithAttribute("method", "GET")); err != nil {
			return err
		}
	}
	if err := client.Gauge(ctx, "queue.depth", 12); err != nil {
		return err
	}
	if err := client.Histogram(ctx, "request.duration", 0.042); err != nil {
		return err
	}
	return client.Flush(ctx)
}

// scrape fetches url once and returns the response body.
func scrape(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return io.ReadAll(resp.Body)
}
