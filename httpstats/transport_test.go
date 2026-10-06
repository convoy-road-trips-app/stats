package httpstats_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats"
	"github.com/convoy-road-trips-app/stats/httpstats"
	"github.com/convoy-road-trips-app/stats/models"
	"github.com/convoy-road-trips-app/stats/statstest"
)

const (
	clientDurationName = "http.client.request.duration"
	clientReqSizeName  = "http.client.request.body.size"
	clientRespSizeName = "http.client.response.body.size"
)

// clientReq builds a request with a context, as noctx requires.
func clientReq(t *testing.T, method, target string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, target, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	return req
}

// newClientServer starts a server running h and returns it.
func newClientServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// instrumented returns an http.Client whose transport records to a fresh
// statstest client, and that client with its capture.
func instrumented(t *testing.T) (hc *http.Client, sc *stats.Client, exp *statstest.Exporter) {
	t.Helper()
	sc, exp = statstest.NewClient(t)
	return &http.Client{Transport: httpstats.NewTransportWith(sc, nil)}, sc, exp
}

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

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

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

func TestClientTransportErrorType(t *testing.T) {
	// Find a port nothing listens on.
	lc := net.ListenConfig{}
	l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	sc, exp := statstest.NewClient(t)
	tr := httpstats.NewTransportWith(sc, &http.Transport{
		DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
	})
	resp, rtErr := tr.RoundTrip(clientReq(t, http.MethodGet, "http://"+addr+"/x", nil))
	if rtErr == nil {
		_ = resp.Body.Close()
		t.Fatal("RoundTrip succeeded, want a dial error")
	}
	var opErr *net.OpError
	if !errors.As(rtErr, &opErr) {
		t.Fatalf("error = %T %v, want the transport's own error returned unchanged (*net.OpError)", rtErr, rtErr)
	}
	statstest.Flush(t, sc)

	d := only(t, exp.Metrics(), clientDurationName)
	wantString(t, d, "error.type", fmt.Sprintf("%T", rtErr))
	wantString(t, d, "http.request.method", "GET")
	wantAbsent(t, d, "http.response.status_code")
	_, port, _ := net.SplitHostPort(addr)
	wantString(t, d, "server.port", port)
	only(t, exp.Metrics(), clientReqSizeName)
	if m := only(t, exp.Metrics(), clientRespSizeName); m.Value != 0 {
		t.Fatalf("response size = %v, want 0 after an error", m.Value)
	}
}

func TestClientErrorReturnedUnchanged(t *testing.T) {
	sc, exp := statstest.NewClient(t)
	boom := errors.New("boom")
	tr := httpstats.NewTransportWith(sc, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, boom
	}))
	resp, err := tr.RoundTrip(clientReq(t, http.MethodGet, "http://example.com", nil))
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err != boom { //nolint:errorlint // identity is the point
		t.Fatalf("error = %v, want the same error value", err)
	}
	statstest.Flush(t, sc)
	wantString(t, only(t, exp.Metrics(), clientDurationName), "error.type", "*errors.errorString")
}

func TestClientServerErrorType(t *testing.T) {
	srv := newClientServer(t, okHandler(http.StatusBadGateway, ""))
	hc, sc, exp := instrumented(t)

	resp, err := hc.Do(clientReq(t, http.MethodGet, srv.URL, nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	statstest.Flush(t, sc)

	d := only(t, exp.Metrics(), clientDurationName)
	wantString(t, d, "http.response.status_code", strconv.Itoa(http.StatusBadGateway))
	wantString(t, d, "error.type", "502")
}

func TestClientClientErrorHasNoErrorType(t *testing.T) {
	srv := newClientServer(t, okHandler(http.StatusNotFound, ""))
	hc, sc, exp := instrumented(t)

	resp, err := hc.Do(clientReq(t, http.MethodGet, srv.URL, nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	statstest.Flush(t, sc)
	wantAbsent(t, only(t, exp.Metrics(), clientDurationName), "error.type")
}

// failingBody yields data, then fails with err.
type failingBody struct {
	data string
	err  error
}

func (b *failingBody) Read(p []byte) (int, error) {
	if b.data == "" {
		return 0, b.err
	}
	n := copy(p, b.data)
	b.data = b.data[n:]
	return n, nil
}

func (*failingBody) Close() error { return nil }

func TestClientBodyReadErrorType(t *testing.T) {
	sc, exp := statstest.NewClient(t)
	readErr := &net.DNSError{Err: "gone"}
	tr := httpstats.NewTransportWith(sc, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: &failingBody{data: "abc", err: readErr}}, nil
	}))
	resp, err := tr.RoundTrip(clientReq(t, http.MethodGet, "http://example.com", nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(resp.Body); !errors.Is(err, readErr) {
		t.Fatalf("read error = %v, want the body's error", err)
	}
	_ = resp.Body.Close()
	statstest.Flush(t, sc)

	wantString(t, only(t, exp.Metrics(), clientDurationName), "error.type", "*net.DNSError")
	if m := only(t, exp.Metrics(), clientRespSizeName); m.Value != 3 {
		t.Fatalf("response size = %v, want 3", m.Value)
	}
}

func TestClientEOFIsNotAnError(t *testing.T) {
	sc, exp := statstest.NewClient(t)
	tr := httpstats.NewTransportWith(sc, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: &failingBody{data: "abc", err: io.EOF}}, nil
	}))
	resp, err := tr.RoundTrip(clientReq(t, http.MethodGet, "http://example.com", nil))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	statstest.Flush(t, sc)
	wantAbsent(t, only(t, exp.Metrics(), clientDurationName), "error.type")
}

func TestClientNilBaseUsesDefault(t *testing.T) {
	var sawReq bool
	prev := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		sawReq = true
		return &http.Response{StatusCode: http.StatusTeapot, Body: http.NoBody}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = prev })

	sc, exp := statstest.NewClient(t)
	for name, tr := range map[string]http.RoundTripper{
		"with":    httpstats.NewTransportWith(sc, nil),
		"default": httpstats.NewTransport(nil),
	} {
		sawReq = false
		httpstats.SetDefaultRecorder(sc)
		resp, err := tr.RoundTrip(clientReq(t, http.MethodGet, "http://example.com", nil))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		_ = resp.Body.Close()
		if !sawReq || resp.StatusCode != http.StatusTeapot {
			t.Fatalf("%s: http.DefaultTransport not used (saw=%v status=%d)", name, sawReq, resp.StatusCode)
		}
	}
	httpstats.SetDefaultRecorder(nil)
	statstest.Flush(t, sc)
	if got := byName(exp.Metrics(), clientDurationName); len(got) != 2 {
		t.Fatalf("durations = %d, want 2", len(got))
	}
}

func TestClientNilRecorderAndUnsetDefaultRecordNothing(t *testing.T) {
	httpstats.SetDefaultRecorder(nil)
	srv := newClientServer(t, okHandler(http.StatusOK, "x"))
	for name, tr := range map[string]http.RoundTripper{
		"default": httpstats.NewTransport(nil),
		"nil":     httpstats.NewTransportWith(nil, nil),
	} {
		resp, err := (&http.Client{Transport: tr}).Do(clientReq(t, http.MethodGet, srv.URL, nil))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if string(b) != "x" {
			t.Fatalf("%s: body = %q, want it passed through", name, b)
		}
	}
}

func TestClientDefaultRecorder(t *testing.T) {
	sc, exp := statstest.NewClient(t)
	httpstats.SetDefaultRecorder(sc)
	t.Cleanup(func() { httpstats.SetDefaultRecorder(nil) })

	srv := newClientServer(t, okHandler(http.StatusOK, "x"))
	resp, err := (&http.Client{Transport: httpstats.NewTransport(nil)}).Do(clientReq(t, http.MethodGet, srv.URL, nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	statstest.Flush(t, sc)
	only(t, exp.Metrics(), clientDurationName)
}

func TestClientRequestBodySizes(t *testing.T) {
	srv := newClientServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	for name, tc := range map[string]struct {
		body io.Reader
		want float64
	}{
		"nil body":     {nil, 0},
		"sized body":   {strings.NewReader(strings.Repeat("a", 1000)), 1000},
		"chunked body": {io.NopCloser(strings.NewReader(strings.Repeat("b", 300))), 300},
	} {
		t.Run(name, func(t *testing.T) {
			hc, sc, exp := instrumented(t)
			resp, err := hc.Do(clientReq(t, http.MethodPut, srv.URL, tc.body))
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			statstest.Flush(t, sc)
			if m := only(t, exp.Metrics(), clientReqSizeName); m.Value != tc.want {
				t.Fatalf("request size = %v, want %v", m.Value, tc.want)
			}
		})
	}
}

func TestClientDoesNotMutateRequest(t *testing.T) {
	srv := newClientServer(t, okHandler(http.StatusOK, "x"))
	sc, _ := statstest.NewClient(t)
	tr := httpstats.NewTransportWith(sc, nil)

	body := io.NopCloser(strings.NewReader("abc"))
	req := clientReq(t, http.MethodPost, srv.URL, body)
	req.Body = body
	ctx := req.Context()
	hdr := req.Header.Clone()

	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if req.Body != body {
		t.Fatal("caller's request Body was replaced")
	}
	if req.Context() != ctx || len(req.Header) != len(hdr) {
		t.Fatal("caller's request was modified")
	}
}

func TestClientContextTagsMerged(t *testing.T) {
	srv := newClientServer(t, okHandler(http.StatusOK, "x"))
	hc, sc, exp := instrumented(t)

	req := httpstats.RequestWithTags(clientReq(t, http.MethodGet, srv.URL, nil), attribute.String("region", "eu"))
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	statstest.Flush(t, sc)

	for _, name := range []string{clientDurationName, clientReqSizeName, clientRespSizeName} {
		wantString(t, only(t, exp.Metrics(), name), "region", "eu")
	}
}

func TestClientCancelledContextStillRecorded(t *testing.T) {
	release := make(chan struct{})
	srv := newClientServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(func() { close(release) })
	hc, sc, exp := instrumented(t)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req = httpstats.RequestWithTags(req, attribute.String("region", "eu"))
	if resp, err := hc.Do(req); err == nil {
		_ = resp.Body.Close()
		t.Fatal("request succeeded, want a timeout")
	}
	statstest.Flush(t, sc)

	d := only(t, exp.Metrics(), clientDurationName)
	if _, ok := attr(d, "error.type"); !ok {
		t.Fatalf("attrs = %v, want error.type", d.Attributes)
	}
	wantString(t, d, "region", "eu")
}

func TestClientUnknownMethodIsOther(t *testing.T) {
	srv := newClientServer(t, okHandler(http.StatusOK, ""))
	hc, sc, exp := instrumented(t)

	resp, err := hc.Do(clientReq(t, "PURGE", srv.URL, nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	statstest.Flush(t, sc)
	wantString(t, only(t, exp.Metrics(), clientDurationName), "http.request.method", "_OTHER")
}
