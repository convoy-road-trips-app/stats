package httpstats_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/convoy-road-trips-app/stats"
	"github.com/convoy-road-trips-app/stats/httpstats"
	"github.com/convoy-road-trips-app/stats/models"
)

func headerHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("X-A", "1")
	w.Header().Set("X-Bb", "22")
	w.WriteHeader(http.StatusOK)
}

func TestServerHeaderMeasurements(t *testing.T) {
	ms := serve(t, func(r stats.Recorder) http.Handler {
		return httpstats.NewHandlerWith(r, http.HandlerFunc(headerHandler))
	}, func(t *testing.T) *http.Request {
		req := newReq(t, http.MethodGet, "/", nil)
		req.Header.Set("X-One", "abc")
		return req
	})

	// "X-One: abc\r\n" is 12 bytes.
	if m := only(t, ms, "http.server.request.header.size"); m.Value != 12 || m.Unit != "By" || m.Type != models.MetricTypeHistogram {
		t.Fatalf("request header size = %+v, want histogram 12 By", m)
	}
	if m := only(t, ms, "http.server.request.header.count"); m.Value != 1 || m.Unit != "{header}" || m.Type != models.MetricTypeHistogram {
		t.Fatalf("request header count = %+v, want histogram 1 {header}", m)
	}
	// "X-A: 1\r\n" (8) + "X-Bb: 22\r\n" (10).
	if m := only(t, ms, "http.server.response.header.size"); m.Value != 18 || m.Unit != "By" {
		t.Fatalf("response header size = %+v, want 18 By", m)
	}
	if m := only(t, ms, "http.server.response.header.count"); m.Value != 2 {
		t.Fatalf("response header count = %+v, want 2", m)
	}
	d := only(t, ms, "http.server.request.header.size")
	wantString(t, d, "http.request.method", "GET")
	wantString(t, d, "http.response.status_code", "200")
}

func TestServerNoRequestCountMetric(t *testing.T) {
	ms := serve(t, func(r stats.Recorder) http.Handler {
		return httpstats.NewHandlerWith(r, okHandler(http.StatusOK, ""))
	}, func(t *testing.T) *http.Request { return newReq(t, http.MethodGet, "/", nil) })
	for _, name := range []string{"http.server.request.count", "http.server.response.count"} {
		if got := byName(ms, name); len(got) != 0 {
			t.Fatalf("%s recorded; the duration histogram count already is the request count", name)
		}
	}
}

func TestServerErrorCounter(t *testing.T) {
	ms := serve(t, func(r stats.Recorder) http.Handler {
		return httpstats.NewHandlerWith(r, okHandler(http.StatusBadGateway, ""))
	}, func(t *testing.T) *http.Request { return newReq(t, http.MethodGet, "/", nil) })
	e := only(t, ms, "http.server.error.count")
	if e.Type != models.MetricTypeCounter || e.Value != 1 || e.Unit != "{error}" {
		t.Fatalf("error counter = %+v, want counter 1 {error}", e)
	}
	wantString(t, e, "error.type", "502")
	wantString(t, e, "http.response.status_code", "502")

	ms = serve(t, func(r stats.Recorder) http.Handler {
		return httpstats.NewHandlerWith(r, okHandler(http.StatusNotFound, ""))
	}, func(t *testing.T) *http.Request { return newReq(t, http.MethodGet, "/", nil) })
	if got := byName(ms, "http.server.error.count"); len(got) != 0 {
		t.Fatalf("404 counted as an error: %v", got)
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

func TestServerBodyReadErrorCounter(t *testing.T) {
	readErr := errors.New("client reset")
	ms := serve(t, func(r stats.Recorder) http.Handler {
		return httpstats.NewHandlerWith(r, http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
			_, _ = io.Copy(io.Discard, req.Body)
		}))
	}, func(t *testing.T) *http.Request {
		return newReq(t, http.MethodPost, "/", errReader{readErr})
	})
	e := only(t, ms, "http.server.error.count")
	wantString(t, e, "error.type", "*errors.errorString")
	// The standard metrics keep their attributes: no error.type for a 200.
	wantAbsent(t, only(t, ms, "http.server.request.duration"), "error.type")
}

func TestServerContentAttributesOptIn(t *testing.T) {
	build := func(opts ...httpstats.Option) func(stats.Recorder) http.Handler {
		return func(r stats.Recorder) http.Handler {
			return httpstats.NewHandlerWith(r, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "Application/JSON; charset=utf-8")
				w.Header().Set("Content-Encoding", "GZIP")
			}), opts...)
		}
	}
	req := func(t *testing.T) *http.Request {
		r := newReq(t, http.MethodPost, "/u/secret-42", strings.NewReader("x"))
		r.Header.Set("Content-Type", "text/plain; charset=utf-8")
		r.Header.Set("Content-Encoding", "br")
		r.TransferEncoding = []string{"chunked"}
		return r
	}

	off := serve(t, build(), req)
	for _, k := range []string{"http.request.header.content_type", "http.response.header.content_type", "http.response.header.content_encoding"} {
		wantAbsent(t, only(t, off, "http.server.request.duration"), k)
	}

	on := serve(t, build(httpstats.WithContentAttributes()), req)
	for _, name := range []string{"http.server.request.duration", "http.server.request.body.size", "http.server.response.header.size"} {
		m := only(t, on, name)
		wantString(t, m, "http.request.header.content_type", "text/plain")
		wantString(t, m, "http.request.header.content_encoding", "br")
		wantString(t, m, "http.request.header.transfer_encoding", "chunked")
		wantString(t, m, "http.response.header.content_type", "application/json")
		wantString(t, m, "http.response.header.content_encoding", "gzip")
	}
}

func TestContentAttributesAreBounded(t *testing.T) {
	ms := serve(t, func(r stats.Recorder) http.Handler {
		return httpstats.NewHandlerWith(r, okHandler(http.StatusOK, ""), httpstats.WithContentAttributes())
	}, func(t *testing.T) *http.Request {
		r := newReq(t, http.MethodGet, "/", nil)
		r.Header.Set("Content-Type", "not a media type")
		r.Header.Set("Content-Encoding", "x-secret-"+strings.Repeat("a", 500))
		r.TransferEncoding = []string{"weird"}
		return r
	})
	m := only(t, ms, "http.server.request.duration")
	wantString(t, m, "http.request.header.content_type", "_OTHER")
	wantString(t, m, "http.request.header.content_encoding", "_OTHER")
	wantString(t, m, "http.request.header.transfer_encoding", "_OTHER")
	wantAbsent(t, m, "http.response.header.content_type")
}

func TestServerHeaderMeasurementsOnPanic(t *testing.T) {
	client, exp := newExp(t)
	h := httpstats.NewHandlerWith(client, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	func() {
		defer func() { _ = recover() }()
		h.ServeHTTP(httptest.NewRecorder(), newReq(t, http.MethodGet, "/", nil))
	}()
	flush(t, client)
	if e := only(t, exp.Metrics(), "http.server.error.count"); e.Value != 1 {
		t.Fatalf("error counter = %+v", e)
	}
	only(t, exp.Metrics(), "http.server.request.header.count")
}
