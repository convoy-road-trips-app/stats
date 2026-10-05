package httpstats

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Inner-writer fixtures: each mixin adds exactly one optional interface.

type innerFlush struct{ flushed int }

func (f *innerFlush) Flush() { f.flushed++ }

type innerHijack struct{ hijacked int }

func (h *innerHijack) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.hijacked++
	return nil, nil, nil
}

type innerReadFrom struct {
	rec  *httptest.ResponseRecorder
	read int64
}

func (r *innerReadFrom) ReadFrom(src io.Reader) (int64, error) {
	n, err := io.Copy(r.rec, src)
	r.read += n
	return n, err
}

type (
	wNone struct{ http.ResponseWriter }
	wF    struct {
		http.ResponseWriter
		*innerFlush
	}
	wH struct {
		http.ResponseWriter
		*innerHijack
	}
	wR struct {
		http.ResponseWriter
		*innerReadFrom
	}
	wFH struct {
		http.ResponseWriter
		*innerFlush
		*innerHijack
	}
	wFR struct {
		http.ResponseWriter
		*innerFlush
		*innerReadFrom
	}
	wHR struct {
		http.ResponseWriter
		*innerHijack
		*innerReadFrom
	}
	wFHR struct {
		http.ResponseWriter
		*innerFlush
		*innerHijack
		*innerReadFrom
	}
)

// plain hides the recorder's own Flush so only the mixins define the method set.
func plain(rec *httptest.ResponseRecorder) http.ResponseWriter {
	return struct{ http.ResponseWriter }{rec}
}

func TestWrapperPreservesMethodSet(t *testing.T) {
	cases := []struct {
		name                string
		build               func(rec *httptest.ResponseRecorder) http.ResponseWriter
		flusher, hij, readF bool
	}{
		{"none", func(r *httptest.ResponseRecorder) http.ResponseWriter { return wNone{plain(r)} }, false, false, false},
		{"flusher", func(r *httptest.ResponseRecorder) http.ResponseWriter { return wF{plain(r), &innerFlush{}} }, true, false, false},
		{"hijacker", func(r *httptest.ResponseRecorder) http.ResponseWriter { return wH{plain(r), &innerHijack{}} }, false, true, false},
		{"readerfrom", func(r *httptest.ResponseRecorder) http.ResponseWriter {
			return wR{plain(r), &innerReadFrom{rec: r}}
		}, false, false, true},
		{"flusher+hijacker", func(r *httptest.ResponseRecorder) http.ResponseWriter {
			return wFH{plain(r), &innerFlush{}, &innerHijack{}}
		}, true, true, false},
		{"flusher+readerfrom", func(r *httptest.ResponseRecorder) http.ResponseWriter {
			return wFR{plain(r), &innerFlush{}, &innerReadFrom{rec: r}}
		}, true, false, true},
		{"hijacker+readerfrom", func(r *httptest.ResponseRecorder) http.ResponseWriter {
			return wHR{plain(r), &innerHijack{}, &innerReadFrom{rec: r}}
		}, false, true, true},
		{"all", func(r *httptest.ResponseRecorder) http.ResponseWriter {
			return wFHR{plain(r), &innerFlush{}, &innerHijack{}, &innerReadFrom{rec: r}}
		}, true, true, true},
	}
	seen := map[string]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			inner := tc.build(rec)
			// Sanity: fixture itself has the expected method set.
			_, f := inner.(http.Flusher)
			_, h := inner.(http.Hijacker)
			_, r := inner.(io.ReaderFrom)
			if f != tc.flusher || h != tc.hij || r != tc.readF {
				t.Fatalf("fixture method set = %v/%v/%v, want %v/%v/%v", f, h, r, tc.flusher, tc.hij, tc.readF)
			}

			w := wrapResponseWriter(inner)
			_, f = w.(http.Flusher)
			_, h = w.(http.Hijacker)
			_, r = w.(io.ReaderFrom)
			if f != tc.flusher {
				t.Errorf("http.Flusher = %v, want %v", f, tc.flusher)
			}
			if h != tc.hij {
				t.Errorf("http.Hijacker = %v, want %v", h, tc.hij)
			}
			if r != tc.readF {
				t.Errorf("io.ReaderFrom = %v, want %v", r, tc.readF)
			}
			if w.Unwrap() != inner {
				t.Errorf("Unwrap() did not return the inner writer")
			}
			seen[typeName(w)] = true
		})
	}
	if len(seen) != 8 {
		t.Errorf("expected 8 distinct concrete wrapper types, got %d: %v", len(seen), seen)
	}
}

func typeName(v any) string { return fmt.Sprintf("%T", v) }

func TestStatusDefaultsTo200OnWrite(t *testing.T) {
	rec := httptest.NewRecorder()
	w := wrapResponseWriter(rec)
	if got := w.status(); got != http.StatusOK {
		t.Fatalf("status before any write = %d, want 200 default", got)
	}
	if _, err := w.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if got := w.status(); got != http.StatusOK {
		t.Errorf("status = %d, want 200", got)
	}
	if got := w.bytesWritten(); got != 5 {
		t.Errorf("bytes = %d, want 5", got)
	}
}

func TestExplicitStatusAndFirstWins(t *testing.T) {
	rec := httptest.NewRecorder()
	w := wrapResponseWriter(rec)
	w.WriteHeader(http.StatusTeapot)
	w.WriteHeader(http.StatusInternalServerError) // ignored by net/http; must be ignored here too
	_, _ = w.Write([]byte("abc"))
	if got := w.status(); got != http.StatusTeapot {
		t.Errorf("status = %d, want 418", got)
	}
	if rec.Code != http.StatusTeapot {
		t.Errorf("inner code = %d, want 418", rec.Code)
	}
}

func TestInformationalStatusNotFinal(t *testing.T) {
	rec := httptest.NewRecorder()
	w := wrapResponseWriter(rec)
	w.WriteHeader(http.StatusEarlyHints)
	_, _ = w.Write([]byte("x"))
	if got := w.status(); got != http.StatusOK {
		t.Errorf("status = %d, want 200 after 103 + Write", got)
	}
}

func TestBytesCountedViaReadFrom(t *testing.T) {
	rec := httptest.NewRecorder()
	ir := &innerReadFrom{rec: rec}
	w := wrapResponseWriter(wR{plain(rec), ir})
	rf, ok := w.(io.ReaderFrom)
	if !ok {
		t.Fatal("wrapper lost io.ReaderFrom")
	}
	n, err := rf.ReadFrom(strings.NewReader("0123456789"))
	if err != nil || n != 10 {
		t.Fatalf("ReadFrom = %d, %v", n, err)
	}
	if ir.read != 10 {
		t.Errorf("inner ReadFrom not used: %d", ir.read)
	}
	if got := w.bytesWritten(); got != 10 {
		t.Errorf("bytes = %d, want 10", got)
	}
	if got := w.status(); got != http.StatusOK {
		t.Errorf("status = %d, want 200 after ReadFrom", got)
	}
	_, _ = w.Write([]byte("ab"))
	if got := w.bytesWritten(); got != 12 {
		t.Errorf("bytes = %d, want 12", got)
	}
}

func TestHijackPassesThrough(t *testing.T) {
	ih := &innerHijack{}
	w := wrapResponseWriter(wH{plain(httptest.NewRecorder()), ih})
	h, ok := w.(http.Hijacker)
	if !ok {
		t.Fatal("wrapper lost http.Hijacker")
	}
	if _, _, err := h.Hijack(); err != nil {
		t.Fatal(err)
	}
	if ih.hijacked != 1 {
		t.Errorf("inner Hijack calls = %d, want 1", ih.hijacked)
	}
}

func TestFlushMarksHeaderWritten(t *testing.T) {
	fl := &innerFlush{}
	w := wrapResponseWriter(wF{plain(httptest.NewRecorder()), fl})
	w.(http.Flusher).Flush()
	if fl.flushed != 1 {
		t.Errorf("inner Flush calls = %d, want 1", fl.flushed)
	}
	w.WriteHeader(http.StatusTeapot) // too late: Flush committed 200
	if got := w.status(); got != http.StatusOK {
		t.Errorf("status = %d, want 200", got)
	}
}

// ctrlWriter implements only FlushError, which http.ResponseController finds via Unwrap.
type ctrlWriter struct {
	http.ResponseWriter
	flushErr int
}

func (c *ctrlWriter) FlushError() error { c.flushErr++; return nil }

func TestUnwrapWorksWithResponseController(t *testing.T) {
	inner := &ctrlWriter{ResponseWriter: httptest.NewRecorder()}
	w := wrapResponseWriter(inner)
	if err := http.NewResponseController(w).Flush(); err != nil {
		t.Fatalf("ResponseController.Flush through Unwrap: %v", err)
	}
	if inner.flushErr != 1 {
		t.Errorf("FlushError calls = %d, want 1", inner.flushErr)
	}
}
