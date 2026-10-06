package httpstats

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats"
)

// Metric names follow the OpenTelemetry HTTP semantic conventions.
const (
	serverDuration     = "http.server.request.duration"
	serverRequestSize  = "http.server.request.body.size"
	serverResponseSize = "http.server.response.body.size"
	serverActive       = "http.server.active_requests"
	serverPrefix       = "http.server"
)

// SetDefaultRecorder sets the recorder that NewHandler records to. Until it is
// called, or after it is called with nil, handlers made by NewHandler record
// nothing. The recorder is looked up on every request, so handlers created
// earlier pick up a later change.
//
// It is safe for concurrent use.
func SetDefaultRecorder(r stats.Recorder) {
	if r == nil {
		defaultRecorder.Store(nil)
		return
	}
	defaultRecorder.Store(&recorderBox{r: r})
}

// NewHandler returns a handler that serves requests with h and records the
// standard HTTP server metrics to the package default recorder, see
// SetDefaultRecorder. Until a default recorder is set it records nothing.
//
// See NewHandlerWith for the metrics and attributes.
func NewHandler(h http.Handler, opts ...Option) http.Handler {
	return &handler{h: h, sink: sink{useDefault: true}, cfg: newConfig(opts)}
}

// NewHandlerWith returns a handler that serves requests with h and records to
// r. A nil r records nothing.
//
// It records, for every request:
//
//   - http.server.request.duration: the handling time in seconds, through
//     stats.DurationObserver when r implements it.
//   - http.server.request.body.size and http.server.response.body.size: the
//     bytes the handler read from the request body and wrote to the response,
//     in bytes.
//   - http.server.request.header.size and http.server.response.header.size:
//     the size of the headers in bytes, counting "Key: value\r\n" per value.
//     The response headers are those set by the handler, not the ones net/http
//     adds while writing (Date, an automatic Content-Length).
//   - http.server.request.header.count and http.server.response.header.count:
//     the number of header values, unit {header}.
//   - http.server.error.count: a counter, recorded with value 1 only for a
//     failed request: status 500 or higher (including a panic), or an error
//     other than io.EOF while the handler read the request body. Its
//     error.type is the status code, or the Go type name of the body error.
//   - http.server.active_requests: a gauge of the requests being handled by
//     this handler, carrying only the request's context tags.
//
// There is no request or response count metric: the sample count of the
// duration histogram already is the number of requests, and the response count
// would be the same number again.
//
// Every metric but the gauge and the error counter's own error.type carries
// these attributes:
//
//   - http.request.method: the request method when it is a known HTTP method,
//     otherwise "_OTHER".
//   - http.response.status_code: the status code (an int), 200 when the handler
//     wrote nothing.
//   - url.scheme: "https" for a TLS request, otherwise "http".
//   - network.protocol.version: for example "1.1" or "2".
//   - http.route: the pattern the request matched, with its method prefix
//     removed ("GET /users/{id}" becomes "/users/{id}"). It is set only when
//     the request matched a pattern, for example through http.ServeMux, and
//     never from the request path.
//   - error.type: the status code as a string, only when it is 500 or higher.
//   - the content attributes of WithContentAttributes, only when that option is
//     given.
//
// The request path and URL are never recorded, because they are unbounded.
//
// Tags attached to the request context with RequestWithTags before the handler
// runs are added to every metric. If h panics, the request is recorded with
// status 500 and error.type "500", and the panic continues.
func NewHandlerWith(r stats.Recorder, h http.Handler, opts ...Option) http.Handler {
	return &handler{h: h, sink: sink{r: r}, cfg: newConfig(opts)}
}

// handler records metrics around the wrapped handler.
type handler struct {
	h    http.Handler
	sink sink
	cfg  config
	// active counts the requests in flight.
	active atomic.Int64
}

// ServeHTTP serves the request with the wrapped handler and records its
// metrics.
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rec := h.sink.recorder()
	start := time.Now()

	// A cancelled request (a client that went away) must still be recorded,
	// but keep the context values, which carry the tags.
	ctx := context.WithoutCancel(r.Context())

	n := h.active.Add(1)
	if rec != nil {
		_ = rec.Gauge(ctx, serverActive, float64(n), stats.WithUnit("{request}"))
	}

	// Shallow-copy the request so replacing the body does not modify the
	// caller's. ServeMux sets Pattern on the request it is given, which is this
	// copy when it sits inside h, so read it from here after serving.
	req := r.WithContext(r.Context())
	var body *countingBody
	if req.Body != nil && req.Body != http.NoBody {
		body = &countingBody{ReadCloser: req.Body}
		req.Body = body
	}
	tw := wrapResponseWriter(w)

	defer func() {
		p := recover()
		left := h.active.Add(-1)
		if rec != nil {
			if p != nil {
				h.record(ctx, rec, req, start, left, body, tw, http.StatusInternalServerError)
			} else {
				h.record(ctx, rec, req, start, left, body, tw, tw.status())
			}
		}
		if p != nil {
			panic(p)
		}
	}()
	h.h.ServeHTTP(tw, req)
}

// record writes the end-of-request metrics: the decremented active gauge and
// the duration and size histograms with status code status.
func (h *handler) record(ctx context.Context, rec stats.Recorder, req *http.Request, start time.Time, active int64, body *countingBody, tw trackedWriter, status int) {
	elapsed := time.Since(start)
	_ = rec.Gauge(ctx, serverActive, float64(active), stats.WithUnit("{request}"))

	scheme := "http"
	if req.TLS != nil {
		scheme = "https"
	}
	attrs := []attribute.KeyValue{
		attribute.String(keyMethod, normalizeMethod(req.Method)),
		attribute.Int(keyStatusCode, status),
		attribute.String(keyScheme, scheme),
		attribute.String(keyProtocolVersion, protocolVersion(req.ProtoMajor, req.ProtoMinor)),
	}
	if route := routeFromPattern(req.Pattern); route != "" {
		attrs = append(attrs, attribute.String(keyRoute, route))
	}
	if status >= http.StatusInternalServerError {
		attrs = append(attrs, attribute.String(keyErrorType, strconv.Itoa(status)))
	}
	if h.cfg.contentAttrs {
		attrs = append(attrs, contentAttrs(req.Header, req.TransferEncoding, keyReqContentType, keyReqContentEncoding, keyReqTransferEncoding)...)
		attrs = append(attrs, contentAttrs(tw.Header(), nil, keyResContentType, keyResContentEncoding, keyResTransferEncoding)...)
	}
	opts := withKeyValues(attrs...)

	var read int64
	var bodyErr error
	if body != nil {
		read, bodyErr = body.count(), body.readErr()
	}
	_ = observeDuration(ctx, rec, serverDuration, elapsed, opts, stats.WithUnit(unitSeconds))
	_ = rec.Histogram(ctx, serverRequestSize, float64(read), opts, stats.WithUnit(unitBytes))
	_ = rec.Histogram(ctx, serverResponseSize, float64(tw.bytesWritten()), opts, stats.WithUnit(unitBytes))
	recordHeaders(ctx, rec, serverPrefix, suffixRequestHeaderSize, suffixRequestHeaderCount, req.Header, opts)
	recordHeaders(ctx, rec, serverPrefix, suffixResponseHeaderSize, suffixResponseHeaderCount, tw.Header(), opts)
	switch {
	case status >= http.StatusInternalServerError:
		recordError(ctx, rec, serverPrefix, opts)
	case bodyErr != nil:
		recordError(ctx, rec, serverPrefix, withKeyValues(append(attrs, attribute.String(keyErrorType, fmt.Sprintf("%T", bodyErr)))...))
	}
}

// routeFromPattern returns pattern without its leading method, so that
// "GET /users/{id}" becomes "/users/{id}". A pattern with no method, or an
// empty one, is returned unchanged.
func routeFromPattern(pattern string) string {
	if method, rest, ok := strings.Cut(pattern, " "); ok && method != "" {
		return strings.TrimLeft(rest, " \t")
	}
	return pattern
}

// RequestWithTags returns a shallow copy of req whose context carries tags in
// addition to the tags it already has. Handlers made by NewHandler and
// NewHandlerWith add them to every metric they record for the request, as
// stats.ContextWithTags describes. Attach them before the handler runs, for
// example in a middleware placed outside it.
//
// Use only low-cardinality values; see stats.ContextWithTags.
func RequestWithTags(req *http.Request, tags ...attribute.KeyValue) *http.Request {
	ctx := req.Context()
	// ContextWithTags replaces rather than inherits, so carry the existing
	// tags over.
	all := append(stats.ContextTags(ctx), tags...)
	return req.WithContext(stats.ContextWithTags(ctx, all...))
}

// RequestTags returns a copy of the tags the request context carries, or nil
// when it has none.
func RequestTags(req *http.Request) []attribute.KeyValue {
	return stats.ContextTags(req.Context())
}
