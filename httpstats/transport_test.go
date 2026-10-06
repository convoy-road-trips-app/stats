package httpstats_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats/httpstats"
	"github.com/convoy-road-trips-app/stats/models"
	"github.com/convoy-road-trips-app/stats/statstest"
)

func TestClientRecordsAttributesAndSizes(t *testing.T) {
	srv := newClientServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, "hello world")
	}))
	hc, sc, exp := instrumented(t)

	resp, err := hc.Do(clientReq(t, http.MethodPost, srv.URL+"/some/path?q=1", strings.NewReader("12345")))
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(resp.Body); string(b) != "hello world" {
		t.Fatalf("body = %q, want it passed through", b)
	}
	_ = resp.Body.Close()
	statstest.Flush(t, sc)

	u, _ := url.Parse(srv.URL)
	d := only(t, exp.Metrics(), clientDurationName)
	if d.Type != models.MetricTypeHistogram || d.Unit != "s" || d.Value <= 0 || d.Value >= 1 {
		t.Fatalf("duration = %+v, want a histogram of a fraction of a second in s", d)
	}
	wantString(t, d, "http.request.method", "POST")
	wantString(t, d, "http.response.status_code", "200")
	wantString(t, d, "server.address", u.Hostname())
	wantString(t, d, "server.port", u.Port())
	wantString(t, d, "url.scheme", "http")
	wantAbsent(t, d, "error.type")
	if v, _ := attr(d, "http.response.status_code"); v.Type() != attribute.INT64 {
		t.Fatalf("status code type = %v, want int64", v.Type())
	}
	if v, _ := attr(d, "server.port"); v.Type() != attribute.INT64 {
		t.Fatalf("server.port type = %v, want int64", v.Type())
	}

	if m := only(t, exp.Metrics(), clientReqSizeName); m.Value != 5 || m.Unit != "By" {
		t.Fatalf("request size = %v %q, want 5 By", m.Value, m.Unit)
	}
	if m := only(t, exp.Metrics(), clientRespSizeName); m.Value != 11 || m.Unit != "By" {
		t.Fatalf("response size = %v %q, want 11 By", m.Value, m.Unit)
	}
}

func TestClientNoURLPathEver(t *testing.T) {
	srv := newClientServer(t, okHandler(http.StatusOK, "x"))
	hc, sc, exp := instrumented(t)

	resp, err := hc.Do(clientReq(t, http.MethodGet, srv.URL+"/users/12345?secret=1", nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	statstest.Flush(t, sc)

	for _, m := range exp.Metrics() {
		for _, kv := range m.Attributes {
			if k := string(kv.Key); k == "url.path" || k == "url.full" || k == "url.query" {
				t.Fatalf("%s carries %s", m.Name, k)
			}
			if strings.Contains(kv.Value.String(), "12345") || strings.Contains(kv.Value.String(), "secret") {
				t.Fatalf("%s: attribute %s=%q leaks the path or query", m.Name, kv.Key, kv.Value.String())
			}
		}
	}
}

func TestClientDefaultPortFromScheme(t *testing.T) {
	sc, exp := statstest.NewClient(t)
	canned := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	})
	tr := httpstats.NewTransportWith(sc, canned)
	for _, target := range []string{"http://example.com/a", "https://example.com/a"} {
		resp, err := tr.RoundTrip(clientReq(t, http.MethodGet, target, nil))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	statstest.Flush(t, sc)

	got := map[string]string{}
	for _, m := range byName(exp.Metrics(), clientDurationName) {
		scheme, _ := attr(m, "url.scheme")
		port, _ := attr(m, "server.port")
		got[scheme.String()] = port.String()
		wantString(t, m, "server.address", "example.com")
	}
	if got["http"] != "80" || got["https"] != "443" {
		t.Fatalf("ports by scheme = %v, want http:80 https:443", got)
	}
}

func TestClientDurationUntilBodyClose(t *testing.T) {
	srv := newClientServer(t, okHandler(http.StatusOK, "payload"))
	hc, sc, exp := instrumented(t)

	resp, err := hc.Do(clientReq(t, http.MethodGet, srv.URL, nil))
	if err != nil {
		t.Fatal(err)
	}
	// Headers have arrived but the body is unread: nothing is recorded yet.
	statstest.Flush(t, sc)
	if got := byName(exp.Metrics(), clientDurationName); len(got) != 0 {
		t.Fatalf("recorded %d durations before the body was closed, want 0", len(got))
	}

	const hold = 60 * time.Millisecond
	time.Sleep(hold)
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	statstest.Flush(t, sc)

	d := only(t, exp.Metrics(), clientDurationName)
	if d.Value < hold.Seconds() {
		t.Fatalf("duration = %vs, want at least the %v the body was held open", d.Value, hold)
	}
	// The body was closed unread, so no response bytes were counted.
	if m := only(t, exp.Metrics(), clientRespSizeName); m.Value != 0 {
		t.Fatalf("response size = %v, want 0 for an unread body", m.Value)
	}
}

func TestClientDurationUntilEOF(t *testing.T) {
	srv := newClientServer(t, okHandler(http.StatusOK, "payload"))
	hc, sc, exp := instrumented(t)

	resp, err := hc.Do(clientReq(t, http.MethodGet, srv.URL, nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatal(err)
	}
	// Recorded at EOF, before Close.
	statstest.Flush(t, sc)
	only(t, exp.Metrics(), clientDurationName)
	if m := only(t, exp.Metrics(), clientRespSizeName); m.Value != 7 {
		t.Fatalf("response size = %v, want 7", m.Value)
	}
	_ = resp.Body.Close()
}

func TestClientRecordsOnce(t *testing.T) {
	srv := newClientServer(t, okHandler(http.StatusOK, "payload"))
	hc, sc, exp := instrumented(t)

	resp, err := hc.Do(clientReq(t, http.MethodGet, srv.URL, nil))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(resp.Body) // EOF
	_ = resp.Body.Close()
	_ = resp.Body.Close()
	_, _ = resp.Body.Read(make([]byte, 1))
	statstest.Flush(t, sc)

	for _, name := range []string{clientDurationName, clientReqSizeName, clientRespSizeName} {
		only(t, exp.Metrics(), name)
	}
}
