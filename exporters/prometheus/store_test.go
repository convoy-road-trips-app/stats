package prometheus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats/models"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func counter(name string, v float64, kv ...attribute.KeyValue) *models.Metric {
	return &models.Metric{Name: name, Type: models.MetricTypeCounter, Value: v, Attributes: kv, Timestamp: t0}
}

func gauge(name string, v float64, ts time.Time) *models.Metric {
	return &models.Metric{Name: name, Type: models.MetricTypeGauge, Value: v, Timestamp: ts}
}

func hist(name string, v float64) *models.Metric {
	return &models.Metric{Name: name, Type: models.MetricTypeHistogram, Value: v, Timestamp: t0}
}

func stats(t *testing.T, h *Handler) string {
	t.Helper()
	var buf bytes.Buffer
	if err := h.WriteStats(&buf); err != nil {
		t.Fatalf("WriteStats: %v", err)
	}
	return buf.String()
}

func export(t *testing.T, h *Handler, ms ...*models.Metric) {
	t.Helper()
	if err := h.Export(context.Background(), ms); err != nil {
		t.Fatalf("Export: %v", err)
	}
}

func TestHandlerName(t *testing.T) {
	var h Handler
	if h.Name() != "prometheus-pull" || h.Name() == "prometheus" {
		t.Fatalf("Name = %q", h.Name())
	}
	if err := h.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCounterTotalOutput(t *testing.T) {
	var h Handler
	m := attribute.String("method", "GET")
	export(t, &h, counter("http.requests", 1, m), counter("http.requests", 2, m))
	out := stats(t, &h)
	for _, want := range []string{"# TYPE http_requests_total counter\n", "http_requests_total{method=\"GET\"} 3\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestTrimPrefix(t *testing.T) {
	h := Handler{TrimPrefix: "app."}
	export(t, &h, counter("app.hits", 1))
	if out := stats(t, &h); !strings.Contains(out, "hits_total 1\n") || strings.Contains(out, "app_") {
		t.Errorf("unexpected:\n%s", out)
	}
}

func TestGaugeOutOfOrderNewestWins(t *testing.T) {
	var h Handler
	export(t, &h, gauge("temp", 20, t0.Add(2*time.Second)))
	export(t, &h, gauge("temp", 10, t0.Add(time.Second))) // older, arrives later
	if out := stats(t, &h); !strings.Contains(out, "temp 20\n") {
		t.Fatalf("older batch overwrote newer:\n%s", out)
	}
	export(t, &h, gauge("temp", 30, t0.Add(2*time.Second))) // tie keeps existing
	if out := stats(t, &h); !strings.Contains(out, "temp 20\n") {
		t.Fatalf("tie replaced existing:\n%s", out)
	}
	export(t, &h, gauge("temp", 40, t0.Add(3*time.Second)))
	if out := stats(t, &h); !strings.Contains(out, "temp 40\n") {
		t.Fatalf("newer did not win:\n%s", out)
	}
}

func TestHistogramOutput(t *testing.T) {
	h := Handler{Buckets: func(string) []float64 { return []float64{1, 5} }}
	export(t, &h, hist("lat", 0.5), hist("lat", 1), hist("lat", 3), hist("lat", 9))
	out := stats(t, &h)
	for _, want := range []string{
		"# TYPE lat histogram\n",
		"lat_bucket{le=\"1\"} 2\n",
		"lat_bucket{le=\"5\"} 3\n",
		"lat_bucket{le=\"+Inf\"} 4\n",
		"lat_sum 13.5\n",
		"lat_count 4\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestHistogramDefaultBuckets(t *testing.T) {
	var h Handler
	export(t, &h, hist("lat", 0.001))
	if out := stats(t, &h); !strings.Contains(out, `lat_bucket{le="0.005"} 1`) {
		t.Errorf("default buckets not used:\n%s", out)
	}
}

func TestMetricTimeoutExpires(t *testing.T) {
	now := t0
	h := Handler{MetricTimeout: time.Minute, now: func() time.Time { return now }}
	export(t, &h, counter("old", 1))
	now = now.Add(45 * time.Second)
	export(t, &h, counter("fresh", 1))
	now = now.Add(30 * time.Second) // old is 75s stale, fresh 30s
	out := stats(t, &h)
	if strings.Contains(out, "old_total") || !strings.Contains(out, "fresh_total 1") {
		t.Fatalf("unexpected:\n%s", out)
	}
	now = now.Add(time.Hour)
	if out := stats(t, &h); out != "" {
		t.Fatalf("want empty, got:\n%s", out)
	}
}

func TestMetricTimeoutDefault(t *testing.T) {
	now := t0
	h := Handler{now: func() time.Time { return now }}
	export(t, &h, counter("c", 1))
	now = now.Add(119 * time.Second)
	if out := stats(t, &h); !strings.Contains(out, "c_total 1") {
		t.Fatalf("expired early:\n%s", out)
	}
	now = now.Add(2 * time.Second)
	if out := stats(t, &h); out != "" {
		t.Fatalf("not expired:\n%s", out)
	}
}

func TestTypeCollisionDropped(t *testing.T) {
	var h Handler
	export(t, &h, gauge("x", 1, t0))
	err := h.Export(context.Background(), []*models.Metric{hist("x", 2), counter("y", 1)})
	if !errors.Is(err, ErrFamilyCollision) || !strings.Contains(err.Error(), `"x"`) {
		t.Fatalf("err = %v", err)
	}
	out := stats(t, &h)
	if !strings.Contains(out, "# TYPE x gauge") || strings.Contains(out, "histogram") || !strings.Contains(out, "y_total 1") {
		t.Fatalf("unexpected:\n%s", out)
	}
}

func TestStoreLabelCollisionWithinObservation(t *testing.T) {
	var h Handler
	err := h.Export(context.Background(), []*models.Metric{
		counter("c", 1, attribute.String("http.method", "GET"), attribute.String("http_method", "POST")),
		counter("ok", 1),
	})
	if !errors.Is(err, ErrLabelCollision) || !strings.Contains(err.Error(), `"c"`) || !strings.Contains(err.Error(), "http_method") {
		t.Fatalf("err = %v", err)
	}
	out := stats(t, &h)
	if strings.Contains(out, "c_total") || !strings.Contains(out, "ok_total 1") {
		t.Fatalf("unexpected:\n%s", out)
	}
}

func TestDistinctAttrsSameExposedSeriesMerge(t *testing.T) {
	var h Handler
	export(t, &h,
		counter("c", 1, attribute.String("http.method", "GET")),
		counter("c", 2, attribute.String("http_method", "GET")),
	)
	out := stats(t, &h)
	if !strings.Contains(out, "c_total{http_method=\"GET\"} 3\n") || strings.Count(out, "c_total{") != 1 {
		t.Fatalf("series not merged:\n%s", out)
	}
}

func TestExportDoesNotRetainMetric(t *testing.T) {
	var h Handler
	m := counter("c", 1, attribute.String("k", "v"))
	export(t, &h, m)
	m.Name, m.Value, m.Attributes[0] = "other", 100, attribute.String("k", "mutated")
	out := stats(t, &h)
	if !strings.Contains(out, "c_total{k=\"v\"} 1\n") || strings.Contains(out, "other") {
		t.Fatalf("store aliased the metric:\n%s", out)
	}
}

func TestInvalidBucketsAndTypes(t *testing.T) {
	h := Handler{Buckets: func(string) []float64 { return []float64{2, 1} }}
	if err := h.Export(context.Background(), []*models.Metric{hist("bad", 1)}); err == nil {
		t.Fatal("want error for non-increasing buckets")
	}
	if err := h.Export(context.Background(), []*models.Metric{{Name: "u", Type: models.MetricType(9)}, nil}); err == nil {
		t.Fatal("want error for unknown type")
	}
	if err := h.Export(context.Background(), []*models.Metric{hist("nan", nanValue())}); err == nil {
		t.Fatal("want error for NaN histogram")
	}
}

func nanValue() float64 {
	var z float64
	return z / z
}

func TestConcurrentExportAndWriteStats(t *testing.T) {
	var h Handler
	var wg sync.WaitGroup
	const writers, perWriter = 4, 200
	for w := range writers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := range perWriter {
				_ = h.Export(context.Background(), []*models.Metric{
					counter("c", 1, attribute.Int("w", w)),
					gauge("g", float64(i), t0.Add(time.Duration(i)*time.Millisecond)),
					hist("h", float64(i)/100),
				})
			}
		}(w)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 100 {
			var buf bytes.Buffer
			if err := h.WriteStats(&buf); err != nil {
				t.Errorf("WriteStats: %v", err)
				return
			}
		}
	}()
	wg.Wait()
	out := stats(t, &h)
	for w := range writers {
		if want := fmt.Sprintf("c_total{w=\"%d\"} %d\n", w, perWriter); !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	if want := fmt.Sprintf("h_count %d\n", writers*perWriter); !strings.Contains(out, want) {
		t.Errorf("missing %q in:\n%s", want, out)
	}
}
