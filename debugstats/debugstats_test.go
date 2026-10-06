package debugstats_test

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats/debugstats"
	"github.com/convoy-road-trips-app/stats/models"
)

var _ models.Exporter = (*debugstats.Exporter)(nil)

func counter(name string, v float64, attrs ...attribute.KeyValue) *models.Metric {
	return &models.Metric{Name: name, Type: models.MetricTypeCounter, Value: v, Attributes: attrs}
}

func TestDebugWritesLines(t *testing.T) {
	var buf bytes.Buffer
	e := &debugstats.Exporter{Dst: &buf}
	if e.Name() != "debugstats" {
		t.Fatalf("Name = %q", e.Name())
	}
	err := e.Export(context.Background(), []*models.Metric{
		counter("server.start", 1),
		counter("http.requests", 2, attribute.String("method", "GET"), attribute.String("status", "200")),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "server.start:1|c\nhttp.requests:2|c|#method:GET,status:200\n"
	if got := buf.String(); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDebugGrepFilters(t *testing.T) {
	var buf bytes.Buffer
	e := &debugstats.Exporter{Dst: &buf, Grep: regexp.MustCompile(`^server\.`)}
	err := e.Export(context.Background(), []*models.Metric{
		counter("server.start", 1),
		counter("db.query", 3),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), "server.start:1|c\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDebugNonMatchingGrepWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	e := &debugstats.Exporter{Dst: &buf, Grep: regexp.MustCompile(`nomatch`)}
	if err := e.Export(context.Background(), []*models.Metric{counter("server.start", 1)}); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("wrote %d bytes: %q", buf.Len(), buf.String())
	}
}

func TestDebugShutdown(t *testing.T) {
	e := &debugstats.Exporter{Dst: &bytes.Buffer{}}
	if err := e.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// slowWriter writes a line in two halves so interleaving would be visible if
// the exporter did not serialize writers.
type slowWriter struct {
	buf bytes.Buffer
}

func (w *slowWriter) Write(p []byte) (int, error) {
	half := len(p) / 2
	w.buf.Write(p[:half])
	w.buf.Write(p[half:])
	return len(p), nil
}

func TestDebugConcurrentNoInterleave(t *testing.T) {
	w := &slowWriter{}
	e := &debugstats.Exporter{Dst: w}
	const goroutines, per = 16, 200
	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range per {
				_ = e.Export(context.Background(), []*models.Metric{
					counter("concurrent.metric", 1, attribute.String("k", "v")),
					counter("concurrent.other", 2),
				})
			}
		}()
	}
	wg.Wait()

	lines := strings.Split(strings.TrimSuffix(w.buf.String(), "\n"), "\n")
	if len(lines) != goroutines*per*2 {
		t.Fatalf("lines = %d, want %d", len(lines), goroutines*per*2)
	}
	for _, l := range lines {
		if l != "concurrent.metric:1|c|#k:v" && l != "concurrent.other:2|c" {
			t.Fatalf("interleaved line %q", l)
		}
	}
}

func TestDebugWriteGoesToDstUnchanged(t *testing.T) {
	var buf bytes.Buffer
	e := &debugstats.Exporter{Dst: &buf}

	n, err := e.Write([]byte("hello\n"))
	if err != nil || n != 6 {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if err := e.Export(context.Background(), []*models.Metric{counter("a", 1)}); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), "hello\na:1|c\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDebugWriteConcurrent(t *testing.T) {
	var buf bytes.Buffer // not safe for concurrent use: Write must serialize
	e := &debugstats.Exporter{Dst: &buf}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				_, _ = e.Write([]byte("x\n"))
			}
		}()
	}
	wg.Wait()
	if got := strings.Count(buf.String(), "x\n"); got != 400 {
		t.Fatalf("got %d lines", got)
	}
}
