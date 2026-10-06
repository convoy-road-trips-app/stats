package httpstats

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats"
)

// Metric names follow the OpenTelemetry HTTP semantic conventions.
const (
	clientDuration     = "http.client.request.duration"
	clientRequestSize  = "http.client.request.body.size"
	clientResponseSize = "http.client.response.body.size"
	clientPrefix       = "http.client"
)

// Client-only attribute keys.
const (
	keyServerAddress = "server.address"
	keyServerPort    = "server.port"
)

// NewTransport returns an http.RoundTripper that sends requests with rt and
// records the standard HTTP client metrics to the package default recorder, see
// SetDefaultRecorder. A nil rt means http.DefaultTransport. Until a default
// recorder is set it records nothing.
//
// See NewTransportWith for the metrics and attributes.
func NewTransport(rt http.RoundTripper, opts ...Option) http.RoundTripper {
	return &transport{rt: rt, sink: sink{useDefault: true}, cfg: newConfig(opts)}
}

// NewTransportWith returns an http.RoundTripper that sends requests with rt and
// records to r. A nil rt means http.DefaultTransport and a nil r records
// nothing.
//
// It records, for every request that is sent:
//
//   - http.client.request.duration: the time from the start of the round trip
//     until the response body is closed or read to EOF (or an error), in
//     seconds, through stats.DurationObserver when r implements it. When the
//     response has no body, or the round trip fails, it ends with the round
//     trip. The caller must therefore close the response body, as net/http
//     already requires.
//   - http.client.request.body.size and http.client.response.body.size: the
//     bytes sent from the request body and read from the response body, in
//     bytes. A request without a body counts 0.
//   - http.client.request.header.size and http.client.response.header.size:
//     the size of the headers in bytes, counting "Key: value\r\n" per value,
//     without the headers net/http adds while writing the request. The
//     response ones are absent when the round trip failed.
//   - http.client.request.header.count and http.client.response.header.count:
//     the number of header values, unit {header}.
//   - http.client.error.count: a counter, recorded with value 1 only for a
//     failed request: the round trip failed, reading the response body failed
//     with anything but io.EOF, or the status is 500 or higher. It has the same
//     attributes, including error.type, as the metrics above.
//
// There is no request or response count metric: the sample count of the
// duration histogram already is the number of requests, and the response count
// would be the same number again.
//
// All of them are recorded once, and carry these attributes:
//
//   - http.request.method: the request method when it is a known HTTP method,
//     otherwise "_OTHER".
//   - http.response.status_code: the status code (an int). Absent when the
//     round trip failed.
//   - server.address and server.port (an int): the host and port the request
//     was sent to; the port defaults from the scheme.
//   - url.scheme: the URL scheme, for example "https".
//   - error.type: the Go type name of the error (for example "*net.OpError")
//     when the round trip fails or reading the response body fails with
//     anything but io.EOF, otherwise the status code as a string when it is
//     500 or higher.
//   - the content attributes of WithContentAttributes, only when that option is
//     given.
//
// The URL path and the full URL are never recorded, because they are
// unbounded. A round trip error is returned unchanged.
//
// Tags attached to the request context, see RequestWithTags, are added to every
// metric. The request passed in is never modified: a copy carries the counting
// body.
func NewTransportWith(r stats.Recorder, rt http.RoundTripper, opts ...Option) http.RoundTripper {
	return &transport{rt: rt, sink: sink{r: r}, cfg: newConfig(opts)}
}

// transport records metrics around the wrapped round tripper.
type transport struct {
	// rt is the wrapped round tripper; nil means http.DefaultTransport, which
	// is looked up on every request.
	rt   http.RoundTripper
	sink sink
	cfg  config
}

// RoundTrip sends req with the wrapped round tripper and records its metrics.
func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.rt
	if base == nil {
		base = http.DefaultTransport
	}
	rec := t.sink.recorder()
	if rec == nil {
		return base.RoundTrip(req)
	}

	start := time.Now()
	// A request cancelled mid-flight must still be recorded, but keep the
	// context values, which carry the tags.
	ctx := context.WithoutCancel(req.Context())

	// Shallow-copy the request so replacing the body does not modify the
	// caller's.
	out := req.WithContext(req.Context())
	var body *countingBody
	if out.Body != nil && out.Body != http.NoBody {
		body = &countingBody{ReadCloser: out.Body}
		out.Body = body
	}

	m := &clientMeasure{
		ctx:     ctx,
		rec:     rec,
		start:   start,
		attrs:   requestAttrs(req),
		reqBody: body,
	}
	// Measure the headers now: they are caller-owned maps that may be mutated
	// after the round trip, so they are never retained.
	m.reqHdr.count, m.reqHdr.size = headerStats(req.Header)
	if t.cfg.contentAttrs {
		m.attrs = append(m.attrs, contentAttrs(req.Header, req.TransferEncoding, keyReqContentType, keyReqContentEncoding, keyReqTransferEncoding)...)
	}

	resp, err := base.RoundTrip(out)
	if err != nil {
		m.finish(err)
		return resp, err
	}
	if resp == nil {
		return nil, nil //nolint:nilnil // a misbehaving RoundTripper's result is passed through
	}
	m.status = resp.StatusCode
	m.resHdr.count, m.resHdr.size = headerStats(resp.Header)
	m.hasResp = true
	if t.cfg.contentAttrs {
		m.attrs = append(m.attrs, contentAttrs(resp.Header, resp.TransferEncoding, keyResContentType, keyResContentEncoding, keyResTransferEncoding)...)
	}
	// A body that is absent, empty, or a two-way stream (101 Switching
	// Protocols) has no read-until-EOF lifetime to measure: record now and leave
	// it untouched so type assertions on it keep working.
	if _, rw := resp.Body.(io.Writer); resp.Body == nil || resp.Body == http.NoBody || rw {
		m.finish(nil)
		return resp, nil
	}
	cb := &countingBody{ReadCloser: resp.Body}
	resp.Body = &clientBody{countingBody: cb, m: m}
	m.respBody = cb
	return resp, nil
}

// requestAttrs returns the attributes known before the response arrives.
func requestAttrs(req *http.Request) []attribute.KeyValue {
	attrs := []attribute.KeyValue{attribute.String(keyMethod, normalizeMethod(req.Method))}
	u := req.URL
	if u == nil {
		return attrs
	}
	attrs = append(attrs, attribute.String(keyServerAddress, u.Hostname()))
	if port, ok := serverPort(u); ok {
		attrs = append(attrs, attribute.Int(keyServerPort, port))
	}
	return append(attrs, attribute.String(keyScheme, u.Scheme))
}

// serverPort returns the port u is sent to: the explicit one, or the default of
// its scheme.
func serverPort(u *url.URL) (int, bool) {
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		return n, err == nil
	}
	switch u.Scheme {
	case "http":
		return 80, true
	case "https":
		return 443, true
	default:
		return 0, false
	}
}

// clientMeasure holds one request's measurement until it is recorded.
type clientMeasure struct {
	// ctx carries the request's context tags to the deferred record.
	ctx   context.Context
	rec   stats.Recorder
	start time.Time
	attrs []attribute.KeyValue
	// status is the response status code, 0 until a response arrives.
	status int
	// reqBody and respBody count the bytes of each body; nil when there is none.
	reqBody  *countingBody
	respBody *countingBody
	// reqHdr and resHdr are the request and response header measurements,
	// taken before the headers can be mutated; hasResp is false when no
	// response arrived.
	reqHdr  headerMeasure
	resHdr  headerMeasure
	hasResp bool
	once    sync.Once
}

// finish records the request's metrics, once. cause is the error that ended
// the response body or the round trip, or nil.
func (m *clientMeasure) finish(cause error) {
	m.once.Do(func() {
		elapsed := time.Since(m.start)
		attrs := m.attrs
		if m.status != 0 {
			attrs = append(attrs, attribute.Int(keyStatusCode, m.status))
		}
		failed := true
		switch {
		case cause != nil && !errors.Is(cause, io.EOF):
			attrs = append(attrs, attribute.String(keyErrorType, fmt.Sprintf("%T", cause)))
		case m.status >= http.StatusInternalServerError:
			attrs = append(attrs, attribute.String(keyErrorType, strconv.Itoa(m.status)))
		default:
			failed = false
		}
		opts := withKeyValues(attrs...)

		var sent, read int64
		if m.reqBody != nil {
			sent = m.reqBody.count()
		}
		if m.respBody != nil {
			read = m.respBody.count()
		}
		_ = observeDuration(m.ctx, m.rec, clientDuration, elapsed, opts, stats.WithUnit(unitSeconds))
		_ = m.rec.Histogram(m.ctx, clientRequestSize, float64(sent), opts, stats.WithUnit(unitBytes))
		_ = m.rec.Histogram(m.ctx, clientResponseSize, float64(read), opts, stats.WithUnit(unitBytes))
		recordHeaderStats(m.ctx, m.rec, clientPrefix, suffixRequestHeaderSize, suffixRequestHeaderCount, m.reqHdr, opts)
		if m.hasResp {
			recordHeaderStats(m.ctx, m.rec, clientPrefix, suffixResponseHeaderSize, suffixResponseHeaderCount, m.resHdr, opts)
		}
		if failed {
			recordError(m.ctx, m.rec, clientPrefix, opts)
		}
	})
}

// clientBody is a response body that records the request's metrics when it
// reaches EOF, fails, or is closed, whichever comes first.
type clientBody struct {
	*countingBody
	m *clientMeasure
}

// Read reads from the response body and records the metrics at EOF or on a
// read error.
func (b *clientBody) Read(p []byte) (int, error) {
	n, err := b.countingBody.Read(p)
	if err != nil {
		b.m.finish(err)
	}
	return n, err
}

// Close closes the response body and records the metrics if that has not
// happened yet.
func (b *clientBody) Close() error {
	err := b.countingBody.Close()
	b.m.finish(nil)
	return err
}
