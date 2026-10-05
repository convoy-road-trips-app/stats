// Package httpstats provides HTTP instrumentation helpers built on the stats
// library.
package httpstats

import (
	"bufio"
	"io"
	"net"
	"net/http"
)

// trackedWriter is an http.ResponseWriter that records the response status and
// the number of body bytes written. Unwrap exposes the inner writer so that
// http.ResponseController can reach optional methods this package does not
// wrap itself (SetWriteDeadline, EnableFullDuplex, FlushError, ...).
type trackedWriter interface {
	http.ResponseWriter
	// Unwrap returns the wrapped http.ResponseWriter.
	Unwrap() http.ResponseWriter
	// status returns the final status code, 200 if nothing was written yet.
	status() int
	// bytesWritten returns the number of body bytes written so far.
	bytesWritten() int64
}

// recorder holds the captured state and implements the methods every wrapper
// shares. It is embedded by pointer in each of the eight concrete wrappers.
type recorder struct {
	inner   http.ResponseWriter
	code    int
	n       int64
	written bool // final header committed (explicitly or implicitly)
}

func (r *recorder) Header() http.Header         { return r.inner.Header() }
func (r *recorder) Unwrap() http.ResponseWriter { return r.inner }
func (r *recorder) bytesWritten() int64         { return r.n }

func (r *recorder) status() int {
	if r.code == 0 {
		return http.StatusOK
	}
	return r.code
}

func (r *recorder) WriteHeader(code int) {
	// 1xx responses other than 101 are informational; the final header
	// follows, so they do not fix the status.
	if !r.written && !(code >= 100 && code < 200 && code != http.StatusSwitchingProtocols) {
		r.code = code
		r.written = true
	}
	r.inner.WriteHeader(code)
}

func (r *recorder) implicitHeader() {
	if !r.written {
		r.code = http.StatusOK
		r.written = true
	}
}

func (r *recorder) Write(p []byte) (int, error) {
	r.implicitHeader()
	n, err := r.inner.Write(p)
	r.n += int64(n)
	return n, err
}

// Optional-interface mixins. Each holds only the recorder and reaches the
// inner writer through it; the type assertions are safe because
// wrapResponseWriter only embeds a mixin when the inner writer has the method.

type flushMixin struct{ t *recorder }

func (m flushMixin) Flush() {
	m.t.implicitHeader()
	m.t.inner.(http.Flusher).Flush()
}

type hijackMixin struct{ t *recorder }

func (m hijackMixin) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return m.t.inner.(http.Hijacker).Hijack()
}

type readFromMixin struct{ t *recorder }

func (m readFromMixin) ReadFrom(src io.Reader) (int64, error) {
	m.t.implicitHeader()
	n, err := m.t.inner.(io.ReaderFrom).ReadFrom(src)
	m.t.n += n
	return n, err
}

// The eight concrete wrapper types, one per subset of
// {http.Flusher, http.Hijacker, io.ReaderFrom}.
type (
	wrapPlain struct{ *recorder }
	wrapF     struct {
		*recorder
		flushMixin
	}
	wrapH struct {
		*recorder
		hijackMixin
	}
	wrapR struct {
		*recorder
		readFromMixin
	}
	wrapFH struct {
		*recorder
		flushMixin
		hijackMixin
	}
	wrapFR struct {
		*recorder
		flushMixin
		readFromMixin
	}
	wrapHR struct {
		*recorder
		hijackMixin
		readFromMixin
	}
	wrapFHR struct {
		*recorder
		flushMixin
		hijackMixin
		readFromMixin
	}
)

// wrapResponseWriter wraps w, capturing the status code and bytes written. The
// returned writer implements exactly the subset of http.Flusher, http.Hijacker
// and io.ReaderFrom that w implements, so direct type assertions on it behave
// as they would on w.
func wrapResponseWriter(w http.ResponseWriter) trackedWriter {
	t := &recorder{inner: w}
	_, f := w.(http.Flusher)
	_, h := w.(http.Hijacker)
	_, r := w.(io.ReaderFrom)
	fm, hm, rm := flushMixin{t}, hijackMixin{t}, readFromMixin{t}
	switch {
	case f && h && r:
		return wrapFHR{t, fm, hm, rm}
	case f && h:
		return wrapFH{t, fm, hm}
	case f && r:
		return wrapFR{t, fm, rm}
	case h && r:
		return wrapHR{t, hm, rm}
	case f:
		return wrapF{t, fm}
	case h:
		return wrapH{t, hm}
	case r:
		return wrapR{t, rm}
	default:
		return wrapPlain{t}
	}
}
