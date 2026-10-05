package httpstats

import (
	"context"
	"io"
	"strconv"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats"
)

// Attribute keys, metric units and the method placeholder follow the
// OpenTelemetry HTTP semantic conventions. The path and URL of a request are
// deliberately absent: they are unbounded and would explode series counts.
const (
	keyMethod          = "http.request.method"
	keyStatusCode      = "http.response.status_code"
	keyScheme          = "url.scheme"
	keyProtocolVersion = "network.protocol.version"
	keyRoute           = "http.route"
	keyErrorType       = "error.type"

	unitSeconds = "s"
	unitBytes   = "By"

	// otherMethod replaces any request method outside the known set.
	otherMethod = "_OTHER"
)

// defaultRecorder holds the recorder used by handlers and transports created
// without an explicit one. It is nil until SetDefaultRecorder is called.
var defaultRecorder atomic.Pointer[recorderBox]

// recorderBox lets atomic.Pointer hold an interface value.
type recorderBox struct{ r stats.Recorder }

// sink resolves the recorder to record to.
type sink struct {
	// r is the explicit recorder; it is used when useDefault is false.
	r stats.Recorder
	// useDefault selects the package default recorder.
	useDefault bool
}

// recorder returns the recorder to use, or nil when nothing should be recorded.
// The default is looked up on every call, so a later SetDefaultRecorder is
// picked up by handlers created earlier.
func (s sink) recorder() stats.Recorder {
	if s.useDefault {
		if b := defaultRecorder.Load(); b != nil {
			return b.r
		}
		return nil
	}
	return s.r
}

// normalizeMethod returns method when it is one of the HTTP methods known to
// the semantic conventions (matched case-sensitively), otherwise "_OTHER".
// This keeps the cardinality of the method attribute bounded.
func normalizeMethod(method string) string {
	switch method {
	case "CONNECT", "DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT", "TRACE":
		return method
	default:
		return otherMethod
	}
}

// protocolVersion formats an HTTP protocol version as network.protocol.version
// does: "1.0" and "1.1", but "2" and "3" without a minor part.
func protocolVersion(major, minor int) string {
	if major >= 2 && minor == 0 {
		return strconv.Itoa(major)
	}
	return strconv.Itoa(major) + "." + strconv.Itoa(minor)
}

// withKeyValues returns a stats.MetricOption that appends typed attributes,
// which stats.WithAttribute cannot do because it only takes strings.
func withKeyValues(kvs ...attribute.KeyValue) stats.MetricOption {
	return func(m *stats.Metric) {
		m.Attributes = append(m.Attributes, kvs...)
	}
}

// observeDuration records d, in seconds, under name. It uses Observe when r
// implements stats.DurationObserver and falls back to Histogram(d.Seconds())
// for recorders written before that interface existed.
func observeDuration(ctx context.Context, r stats.Recorder, name string, d time.Duration, opts ...stats.MetricOption) error {
	if o, ok := r.(stats.DurationObserver); ok {
		return o.Observe(ctx, name, d, opts...)
	}
	return r.Histogram(ctx, name, d.Seconds(), opts...)
}

// countingBody counts the bytes read through an io.ReadCloser.
type countingBody struct {
	io.ReadCloser
	n atomic.Int64
}

// Read reads from the wrapped body and adds the bytes read to the total.
func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.n.Add(int64(n))
	return n, err
}

// count returns the number of bytes read so far.
func (b *countingBody) count() int64 { return b.n.Load() }
