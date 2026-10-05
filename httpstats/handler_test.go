package httpstats_test

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats"
	"github.com/convoy-road-trips-app/stats/httpstats"
	"github.com/convoy-road-trips-app/stats/models"
	"github.com/convoy-road-trips-app/stats/statstest"
)

// serve sends a request with the given method and target through h and returns
// the metrics the client exported.
func serve(t *testing.T, build func(stats.Recorder) http.Handler, req func(*testing.T) *http.Request) []models.Metric {
	t.Helper()
	client, exp := statstest.NewClient(t)
	h := build(client)
	h.ServeHTTP(httptest.NewRecorder(), req(t))
	statstest.Flush(t, client)
	return exp.Metrics()
}

// newReq builds a request with a context, as noctx requires.
func newReq(t *testing.T, method, target string, body io.Reader) *http.Request {
	t.Helper()
	return httptest.NewRequestWithContext(context.Background(), method, target, body)
}

// byName returns the captured metrics called name.
func byName(ms []models.Metric, name string) []*models.Metric {
	var out []*models.Metric
	for i := range ms {
		if ms[i].Name == name {
			out = append(out, &ms[i])
		}
	}
	return out
}

// only returns the single metric called name and fails when there is not
// exactly one.
func only(t *testing.T, ms []models.Metric, name string) *models.Metric {
	t.Helper()
	got := byName(ms, name)
	if len(got) != 1 {
		t.Fatalf("metric %q: got %d, want 1 (all: %v)", name, len(got), ms)
	}
	return got[0]
}

// attr returns the value of attribute key of m.
func attr(m *models.Metric, key string) (attribute.Value, bool) {
	for _, kv := range m.Attributes {
		if string(kv.Key) == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

func wantString(t *testing.T, m *models.Metric, key, want string) {
	t.Helper()
	v, ok := attr(m, key)
	if !ok {
		t.Fatalf("%s: attribute %q missing (attrs %v)", m.Name, key, m.Attributes)
	}
	if got := v.String(); got != want {
		t.Fatalf("%s: %s = %q, want %q", m.Name, key, got, want)
	}
}

func wantAbsent(t *testing.T, m *models.Metric, key string) {
	t.Helper()
	if v, ok := attr(m, key); ok {
		t.Fatalf("%s: attribute %q = %q, want it absent", m.Name, key, v.String())
	}
}

func okHandler(status int, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	})
}

func TestServerDurationSeconds(t *testing.T) {
	ms := serve(t, func(r stats.Recorder) http.Handler {
		return httpstats.NewHandlerWith(r, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			// The handler's own time is what is measured.
			w.WriteHeader(http.StatusAccepted)
		}))
	}, func(t *testing.T) *http.Request { return newReq(t, http.MethodGet, "/", nil) })

	d := only(t, ms, "http.server.request.duration")
	if d.Type != models.MetricTypeHistogram {
		t.Fatalf("type = %v, want histogram", d.Type)
	}
	// Seconds, not milliseconds: an instant handler is far below one second.
	if d.Value <= 0 || d.Value >= 1 {
		t.Fatalf("duration = %v, want a fraction of a second", d.Value)
	}
	if d.Unit != "s" {
		t.Fatalf("unit = %q, want s", d.Unit)
	}
	wantString(t, d, "http.request.method", "GET")
	wantString(t, d, "http.response.status_code", "202")
	wantString(t, d, "url.scheme", "http")
	wantString(t, d, "network.protocol.version", "1.1")
	wantAbsent(t, d, "error.type")
	if v, _ := attr(d, "http.response.status_code"); v.Type() != attribute.INT64 {
		t.Fatalf("status code type = %v, want int64", v.Type())
	}
}

func TestDefaultStatusAndHTTPS(t *testing.T) {
	ms := serve(t, func(r stats.Recorder) http.Handler {
		return httpstats.NewHandlerWith(r, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	}, func(t *testing.T) *http.Request {
		req := newReq(t, http.MethodGet, "https://example.com/", nil)
		req.Proto, req.ProtoMajor, req.ProtoMinor = "HTTP/2.0", 2, 0
		return req
	})
	d := only(t, ms, "http.server.request.duration")
	wantString(t, d, "http.response.status_code", "200")
	wantString(t, d, "url.scheme", "https")
	wantString(t, d, "network.protocol.version", "2")
}

func TestRouteFromPattern(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("GET /users/{id}", okHandler(http.StatusOK, ""))
	ms := serve(t, func(r stats.Recorder) http.Handler {
		return httpstats.NewHandlerWith(r, mux)
	}, func(t *testing.T) *http.Request { return newReq(t, http.MethodGet, "/users/42", nil) })

	d := only(t, ms, "http.server.request.duration")
	wantString(t, d, "http.route", "/users/{id}")
}

func TestRouteFromPatternInsideMux(t *testing.T) {
	var h http.Handler
	client, exp := statstest.NewClient(t)
	mux := http.NewServeMux()
	mux.Handle("POST /orders", httpstats.NewHandlerWith(client, okHandler(http.StatusCreated, "")))
	h = mux
	h.ServeHTTP(httptest.NewRecorder(), newReq(t, http.MethodPost, "/orders", nil))
	statstest.Flush(t, client)

	wantString(t, only(t, exp.Metrics(), "http.server.request.duration"), "http.route", "/orders")
}

func TestNoRouteAttrWhenUnset(t *testing.T) {
	ms := serve(t, func(r stats.Recorder) http.Handler {
		return httpstats.NewHandlerWith(r, okHandler(http.StatusOK, ""))
	}, func(t *testing.T) *http.Request { return newReq(t, http.MethodGet, "/anything", nil) })

	for _, name := range []string{"http.server.request.duration", "http.server.request.body.size", "http.server.response.body.size"} {
		wantAbsent(t, only(t, ms, name), "http.route")
	}
}

func TestNoURLPathEver(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("GET /users/{id}", okHandler(http.StatusOK, "x"))
	ms := serve(t, func(r stats.Recorder) http.Handler {
		return httpstats.NewHandlerWith(r, mux)
	}, func(t *testing.T) *http.Request {
		return newReq(t, http.MethodGet, "http://example.com/users/secret-42?token=abc", nil)
	})

	for i := range ms {
		m := &ms[i]
		for _, kv := range m.Attributes {
			k := string(kv.Key)
			if strings.HasPrefix(k, "url.path") || k == "url.full" || k == "url.query" {
				t.Fatalf("%s carries %s", m.Name, k)
			}
			if v := kv.Value.String(); strings.Contains(v, "secret-42") || strings.Contains(v, "token") || strings.Contains(v, "example.com") {
				t.Fatalf("%s attribute %s = %q leaks the request URL", m.Name, k, v)
			}
		}
	}
}

func TestUnknownMethodIsOther(t *testing.T) {
	for method, want := range map[string]string{
		"GET":      "GET",
		"PATCH":    "PATCH",
		"get":      "_OTHER",
		"PROPFIND": "_OTHER",
		"BREW":     "_OTHER",
	} {
		ms := serve(t, func(r stats.Recorder) http.Handler {
			return httpstats.NewHandlerWith(r, okHandler(http.StatusOK, ""))
		}, func(t *testing.T) *http.Request {
			req := newReq(t, http.MethodGet, "/", nil)
			req.Method = method
			return req
		})
		wantString(t, only(t, ms, "http.server.request.duration"), "http.request.method", want)
	}
}

func TestBodySizes(t *testing.T) {
	ms := serve(t, func(r stats.Recorder) http.Handler {
		return httpstats.NewHandlerWith(r, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if _, err := io.Copy(io.Discard, req.Body); err != nil {
				t.Errorf("read body: %v", err)
			}
			_, _ = io.WriteString(w, "hello world")
		}))
	}, func(t *testing.T) *http.Request { return newReq(t, http.MethodPost, "/", strings.NewReader("12345")) })

	in := only(t, ms, "http.server.request.body.size")
	out := only(t, ms, "http.server.response.body.size")
	if in.Value != 5 || in.Unit != "By" || in.Type != models.MetricTypeHistogram {
		t.Fatalf("request size = %+v, want histogram 5 By", in)
	}
	if out.Value != 11 || out.Unit != "By" || out.Type != models.MetricTypeHistogram {
		t.Fatalf("response size = %+v, want histogram 11 By", out)
	}
	wantString(t, in, "http.request.method", "POST")
	wantString(t, out, "http.response.status_code", "200")
}

func TestServerErrorType(t *testing.T) {
	ms := serve(t, func(r stats.Recorder) http.Handler {
		return httpstats.NewHandlerWith(r, okHandler(http.StatusBadGateway, ""))
	}, func(t *testing.T) *http.Request { return newReq(t, http.MethodGet, "/", nil) })
	wantString(t, only(t, ms, "http.server.request.duration"), "error.type", "502")

	ms = serve(t, func(r stats.Recorder) http.Handler {
		return httpstats.NewHandlerWith(r, okHandler(http.StatusNotFound, ""))
	}, func(t *testing.T) *http.Request { return newReq(t, http.MethodGet, "/", nil) })
	wantAbsent(t, only(t, ms, "http.server.request.duration"), "error.type")
}

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
