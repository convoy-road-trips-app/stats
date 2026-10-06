package httpstats

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats"
)

// Header measurement and error counter metric names; the server and client
// names are built from these suffixes in handler.go and transport.go.
const (
	suffixRequestHeaderSize   = ".request.header.size"
	suffixRequestHeaderCount  = ".request.header.count"
	suffixResponseHeaderSize  = ".response.header.size"
	suffixResponseHeaderCount = ".response.header.count"

	unitHeaders = "{header}"
	unitErrors  = "{error}"
)

// Content attribute keys, named after the OpenTelemetry
// http.request.header.<key> and http.response.header.<key> attributes, with the
// "-" of a header name written as "_" because tag keys cannot contain "-".
const (
	keyReqContentType      = "http.request.header.content_type"
	keyReqContentEncoding  = "http.request.header.content_encoding"
	keyReqTransferEncoding = "http.request.header.transfer_encoding"
	keyResContentType      = "http.response.header.content_type"
	keyResContentEncoding  = "http.response.header.content_encoding"
	keyResTransferEncoding = "http.response.header.transfer_encoding"

	// maxMediaTypeLen bounds a recorded content type.
	maxMediaTypeLen = 64

	// otherValue replaces a content value outside the bounded set.
	otherValue = "_OTHER"
)

// config holds the settings of Option.
type config struct{ contentAttrs bool }

// Option configures NewHandler, NewHandlerWith, NewTransport and
// NewTransportWith.
type Option func(*config)

// WithContentAttributes adds content metadata attributes to every metric of a
// request, for both the request and the response:
//
//   - http.request.header.content_type and http.response.header.content_type:
//     the media type, lower-cased and without parameters such as charset.
//   - http.request.header.content_encoding and
//     http.response.header.content_encoding: for example "gzip".
//   - http.request.header.transfer_encoding and
//     http.response.header.transfer_encoding: "chunked" or "identity".
//
// An attribute is left out when the header is absent, and set to "_OTHER" when
// its value is not a well-formed media type or a known coding, so that a client
// cannot make the values unbounded. The option is off by default because every
// distinct value multiplies the number of series, and the content type of
// requests is chosen by the peer: enable it where the set of content types is
// small and known.
func WithContentAttributes() Option {
	return func(c *config) { c.contentAttrs = true }
}

// newConfig applies opts to a zero config.
func newConfig(opts []Option) config {
	var c config
	for _, o := range opts {
		if o != nil {
			o(&c)
		}
	}
	return c
}

// headerStats returns the number of header values in h and their approximate
// size on the wire: each value counts "Key: value\r\n". It does not include the
// request or status line, nor headers added by net/http itself while writing.
func headerStats(h http.Header) (count, size int64) {
	for k, vs := range h {
		for _, v := range vs {
			count++
			size += int64(len(k) + len(": ") + len(v) + len("\r\n"))
		}
	}
	return count, size
}

// recordHeaders records the header count and size histograms for one direction
// (suffixes name them) under prefix ("http.server" or "http.client").
func recordHeaders(ctx context.Context, rec stats.Recorder, prefix, sizeSuffix, countSuffix string, h http.Header, opts stats.MetricOption) {
	count, size := headerStats(h)
	_ = rec.Histogram(ctx, prefix+sizeSuffix, float64(size), opts, stats.WithUnit(unitBytes))
	_ = rec.Histogram(ctx, prefix+countSuffix, float64(count), opts, stats.WithUnit(unitHeaders))
}

// recordError records the error counter prefix+".error.count" with one failure.
func recordError(ctx context.Context, rec stats.Recorder, prefix string, opts stats.MetricOption) {
	_ = rec.Counter(ctx, prefix+".error.count", 1, opts, stats.WithUnit(unitErrors))
}

// contentAttrs returns the content metadata attributes for one direction.
// typeKey, encKey and teKey are the attribute keys; te is the message's
// transfer encoding list.
func contentAttrs(h http.Header, te []string, typeKey, encKey, teKey string) []attribute.KeyValue {
	var out []attribute.KeyValue
	if v := h.Get("Content-Type"); v != "" {
		out = append(out, attribute.String(typeKey, mediaType(v)))
	}
	if v := h.Get("Content-Encoding"); v != "" {
		out = append(out, attribute.String(encKey, knownOrOther(v, contentCodings)))
	}
	if len(te) > 0 {
		out = append(out, attribute.String(teKey, knownOrOther(strings.Join(te, ","), transferCodings)))
	}
	return out
}

var (
	contentCodings  = []string{"gzip", "deflate", "br", "zstd", "compress", "identity"}
	transferCodings = []string{"chunked", "identity"}
)

// knownOrOther returns v lower-cased when it is one of known, else "_OTHER".
func knownOrOther(v string, known []string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	for _, k := range known {
		if v == k {
			return v
		}
	}
	return otherValue
}

// mediaType returns the lower-cased media type of a Content-Type value without
// its parameters, or "_OTHER" when it is malformed or longer than
// maxMediaTypeLen.
func mediaType(v string) string {
	mt, _, err := mime.ParseMediaType(v)
	// A malformed parameter does not make the type itself unusable.
	if mt == "" || (err != nil && !errors.Is(err, mime.ErrInvalidMediaParameter)) || len(mt) > maxMediaTypeLen {
		return otherValue
	}
	return mt
}
