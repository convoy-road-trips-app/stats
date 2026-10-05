package prometheus

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(t *testing.T, h *Handler, method, acceptEncoding string) *http.Response {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, "/metrics", nil)
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestServeGet(t *testing.T) {
	var h Handler
	export(t, &h, counter("http.requests", 3))
	resp := serve(t, &h, http.MethodGet, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/plain; version=0.0.4; charset=utf-8" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if resp.Header.Get("Content-Encoding") != "" {
		t.Fatal("unexpected Content-Encoding")
	}
	if !strings.Contains(resp.Header.Get("Vary"), "Accept-Encoding") {
		t.Fatalf("Vary = %q", resp.Header.Get("Vary"))
	}
	body := readAll(t, resp)
	if body != stats(t, &h) {
		t.Fatalf("body = %q", body)
	}
}

func TestGzip(t *testing.T) {
	var h Handler
	export(t, &h, counter("http.requests", 3))
	resp := serve(t, &h, http.MethodGet, "deflate, gzip;q=0.8")
	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q", resp.Header.Get("Content-Encoding"))
	}
	if !strings.Contains(resp.Header.Get("Vary"), "Accept-Encoding") {
		t.Fatalf("Vary = %q", resp.Header.Get("Vary"))
	}
	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != stats(t, &h) {
		t.Fatalf("gunzipped body = %q", plain)
	}
}

func TestGzipRefusedByQualityZero(t *testing.T) {
	var h Handler
	export(t, &h, counter("c", 1))
	for _, ae := range []string{"gzip;q=0", "identity", "gzipped"} {
		resp := serve(t, &h, http.MethodGet, ae)
		if enc := resp.Header.Get("Content-Encoding"); enc != "" {
			t.Fatalf("Accept-Encoding %q: Content-Encoding = %q", ae, enc)
		}
		_ = readAll(t, resp)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	var h Handler
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		resp := serve(t, &h, m, "")
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("%s: status = %d", m, resp.StatusCode)
		}
		if allow := resp.Header.Get("Allow"); allow != "GET, HEAD" {
			t.Fatalf("%s: Allow = %q", m, allow)
		}
		_ = readAll(t, resp)
	}
}

func TestHead(t *testing.T) {
	var h Handler
	export(t, &h, counter("http.requests", 3))
	resp := serve(t, &h, http.MethodHead, "gzip")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if resp.Header.Get("Content-Type") != ContentType || resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("headers = %v", resp.Header)
	}
	if body := readAll(t, resp); body != "" {
		t.Fatalf("HEAD body = %q", body)
	}
}
