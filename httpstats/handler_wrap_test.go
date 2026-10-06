package httpstats_test

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats/httpstats"
	"github.com/convoy-road-trips-app/stats/models"
	"github.com/convoy-road-trips-app/stats/statstest"
)

func TestPanicRecordedAndRepanicked(t *testing.T) {
	client, exp := statstest.NewClient(t)
	h := httpstats.NewHandlerWith(client, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	func() {
		defer func() {
			if got := recover(); got != "boom" {
				t.Fatalf("recovered %v, want the original panic value", got)
			}
		}()
		h.ServeHTTP(httptest.NewRecorder(), newReq(t, http.MethodGet, "/", nil))
	}()

	statstest.Flush(t, client)
	d := only(t, exp.Metrics(), "http.server.request.duration")
	wantString(t, d, "http.response.status_code", "500")
	wantString(t, d, "error.type", "500")
}

func TestActiveRequests(t *testing.T) {
	client, exp := statstest.NewClient(t)
	var during float64
	h := httpstats.NewHandlerWith(client, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		statstest.Flush(t, client)
		for _, m := range byName(exp.Metrics(), "http.server.active_requests") {
			during = m.Value
		}
	}))
	h.ServeHTTP(httptest.NewRecorder(), newReq(t, http.MethodGet, "/", nil))
	statstest.Flush(t, client)

	if during != 1 {
		t.Fatalf("active during request = %v, want 1", during)
	}
	gauges := byName(exp.Metrics(), "http.server.active_requests")
	if len(gauges) != 2 {
		t.Fatalf("got %d active_requests observations, want 2 (start and end)", len(gauges))
	}
	if last := gauges[len(gauges)-1]; last.Type != models.MetricTypeGauge || last.Value != 0 {
		t.Fatalf("last active_requests = %+v, want gauge 0", last)
	}
}

func TestNilRecorderAndUnsetDefaultRecordNothing(t *testing.T) {
	httpstats.SetDefaultRecorder(nil)
	for name, h := range map[string]http.Handler{
		"default": httpstats.NewHandler(okHandler(http.StatusOK, "x")),
		"nil":     httpstats.NewHandlerWith(nil, okHandler(http.StatusOK, "x")),
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, newReq(t, http.MethodGet, "/", nil))
		if rec.Code != http.StatusOK || rec.Body.String() != "x" {
			t.Fatalf("%s: response = %d %q, want it passed through", name, rec.Code, rec.Body.String())
		}
	}
}

func TestDefaultRecorder(t *testing.T) {
	client, exp := statstest.NewClient(t)
	httpstats.SetDefaultRecorder(client)
	t.Cleanup(func() { httpstats.SetDefaultRecorder(nil) })

	h := httpstats.NewHandler(okHandler(http.StatusOK, ""))
	h.ServeHTTP(httptest.NewRecorder(), newReq(t, http.MethodGet, "/", nil))
	statstest.Flush(t, client)

	only(t, exp.Metrics(), "http.server.request.duration")
}

// hijackWriter is a response writer that can be hijacked.
type hijackWriter struct {
	http.ResponseWriter
	hijacked int
}

func (h *hijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.hijacked++
	return nil, nil, nil
}

func TestHijackerPreserved(t *testing.T) {
	client, _ := statstest.NewClient(t)
	var gotHijacker, gotFlusher bool
	h := httpstats.NewHandlerWith(client, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		gotHijacker = ok
		if ok {
			_, _, _ = hj.Hijack()
		}
		_, gotFlusher = w.(http.Flusher)
	}))

	hw := &hijackWriter{ResponseWriter: struct{ http.ResponseWriter }{httptest.NewRecorder()}}
	h.ServeHTTP(hw, newReq(t, http.MethodGet, "/", nil))
	if !gotHijacker || hw.hijacked != 1 {
		t.Fatalf("hijacker = %v, hijacked = %d, want the inner Hijack to be reachable", gotHijacker, hw.hijacked)
	}
	if gotFlusher {
		t.Fatal("writer exposes http.Flusher the inner writer lacks")
	}
}

func TestFlusherPreserved(t *testing.T) {
	client, _ := statstest.NewClient(t)
	var gotFlusher bool
	h := httpstats.NewHandlerWith(client, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, gotFlusher = w.(http.Flusher)
	}))
	h.ServeHTTP(httptest.NewRecorder(), newReq(t, http.MethodGet, "/", nil))
	if !gotFlusher {
		t.Fatal("http.Flusher of the inner writer was lost")
	}
}

func TestRequestTagsMerged(t *testing.T) {
	req := newReq(t, http.MethodGet, "/", nil)
	req = httpstats.RequestWithTags(req, attribute.String("region", "eu"))
	req = httpstats.RequestWithTags(req, attribute.String("tier", "gold"))

	tags := httpstats.RequestTags(req)
	if len(tags) != 2 || tags[0].Key != "region" || tags[1].Key != "tier" {
		t.Fatalf("RequestTags = %v, want region then tier (existing tags kept)", tags)
	}
	if got := httpstats.RequestTags(newReq(t, http.MethodGet, "/", nil)); got != nil {
		t.Fatalf("RequestTags on a plain request = %v, want nil", got)
	}

	client, exp := statstest.NewClient(t)
	httpstats.NewHandlerWith(client, okHandler(http.StatusOK, "x")).ServeHTTP(httptest.NewRecorder(), req)
	statstest.Flush(t, client)

	for _, name := range []string{
		"http.server.request.duration",
		"http.server.request.body.size",
		"http.server.response.body.size",
	} {
		m := only(t, exp.Metrics(), name)
		wantString(t, m, "region", "eu")
		wantString(t, m, "tier", "gold")
	}
	for _, m := range byName(exp.Metrics(), "http.server.active_requests") {
		wantString(t, m, "region", "eu")
	}
}
