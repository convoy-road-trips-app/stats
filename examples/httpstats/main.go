// Command httpstats shows HTTP server and client instrumentation: a handler
// wrapped with httpstats.NewHandlerWith and an http.Client whose transport is
// wrapped with httpstats.NewTransportWith, talking over a loopback listener.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"time"

	"github.com/convoy-road-trips-app/stats"
	"github.com/convoy-road-trips-app/stats/debugstats"
	"github.com/convoy-road-trips-app/stats/httpstats"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	client, err := stats.NewClient(
		stats.WithServiceName("httpstats-example"),
		stats.WithVersionReporting(false),
		stats.WithFlushInterval(time.Hour),
		// Print only the duration histograms to keep the output short.
		stats.WithExporter(&debugstats.Exporter{
			Dst:  os.Stdout,
			Grep: regexp.MustCompile(`^http\.(server|client)\.request\.duration`),
		}),
	)
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}
	defer func() { _ = client.Close() }()

	// Server side: wrap the handler. The route attribute comes from the
	// ServeMux pattern, never from the raw path.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "hello")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: httpstats.NewHandlerWith(client, mux), ReadHeaderTimeout: 2 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "serve:", err)
		}
	}()
	defer func() { _ = srv.Shutdown(context.Background()) }()

	// Client side: wrap the transport.
	hc := &http.Client{Transport: httpstats.NewTransportWith(client, nil), Timeout: 2 * time.Second}

	base := "http://" + ln.Addr().String()
	for _, path := range []string{"/users/42", "/users/43", "/missing"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, http.NoBody)
		if err != nil {
			return err
		}
		resp, err := hc.Do(req)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close() // the client duration is recorded when the body is closed
		fmt.Println("GET", path, "->", resp.StatusCode)
	}

	return client.Flush(ctx)
}
