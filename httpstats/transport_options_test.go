package httpstats_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats/httpstats"
	"github.com/convoy-road-trips-app/stats/statstest"
)

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
