package httpstats_test

import (
	"io"
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
