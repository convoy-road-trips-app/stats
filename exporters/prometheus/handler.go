package prometheus

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"strconv"
	"strings"
)

// ContentType is the Content-Type of a scrape response: the Prometheus text
// exposition format, version 0.0.4.
const ContentType = "text/plain; version=0.0.4; charset=utf-8"

var _ http.Handler = (*Handler)(nil)

// ServeHTTP serves a scrape of the store, see WriteStats. Only GET and HEAD
// are allowed; any other method gets 405 with an Allow header. The response has
// Content-Type ContentType and is gzip-compressed when the request's
// Accept-Encoding lists gzip (the response then always carries
// "Vary: Accept-Encoding"). HEAD sends the headers of the equivalent GET and no
// body.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Vary", "Accept-Encoding")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body bytes.Buffer
	if err := h.WriteStats(&body); err != nil {
		http.Error(w, "render metrics: "+err.Error(), http.StatusInternalServerError)
		return
	}

	out := body.Bytes()
	if acceptsGzip(r.Header.Values("Accept-Encoding")) {
		var zipped bytes.Buffer
		zw := gzip.NewWriter(&zipped)
		if _, err := zw.Write(out); err != nil {
			http.Error(w, "compress metrics: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if err := zw.Close(); err != nil {
			http.Error(w, "compress metrics: "+err.Error(), http.StatusInternalServerError)
			return
		}
		out = zipped.Bytes()
		w.Header().Set("Content-Encoding", "gzip")
	}

	w.Header().Set("Content-Type", ContentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(out)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(out)
}

// acceptsGzip reports whether the Accept-Encoding header values list gzip with
// a non-zero quality.
func acceptsGzip(values []string) bool {
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			coding, params, _ := strings.Cut(strings.TrimSpace(part), ";")
			if !strings.EqualFold(strings.TrimSpace(coding), "gzip") {
				continue
			}
			if q, ok := qualityOf(params); ok && q <= 0 {
				continue
			}
			return true
		}
	}
	return false
}

// qualityOf extracts the q parameter from the parameters of an
// Accept-Encoding element.
func qualityOf(params string) (float64, bool) {
	for _, p := range strings.Split(params, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(k), "q") {
			continue
		}
		q, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, false
		}
		return q, true
	}
	return 0, false
}
