package httpstats_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/convoy-road-trips-app/stats/httpstats"
	"github.com/convoy-road-trips-app/stats/models"
	"github.com/convoy-road-trips-app/stats/statstest"
)

func TestClientHeaderMeasurements(t *testing.T) {
	srv := newClientServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-A", "1")
		w.Header().Set("Content-Length", "0")
	}))
	hc, sc, exp := instrumented(t)
	req := clientReq(t, http.MethodGet, srv.URL, nil)
	req.Header.Set("X-One", "abc")
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	want := 0
	for k, vs := range resp.Header {
		for _, v := range vs {
			want += len(k) + len(": ") + len(v) + len("\r\n")
		}
	}
	wantCount := len(resp.Header)
	_ = resp.Body.Close()
	statstest.Flush(t, sc)

	// "X-One: abc\r\n" is 12 bytes.
	if m := only(t, exp.Metrics(), "http.client.request.header.size"); m.Value != 12 || m.Unit != "By" || m.Type != models.MetricTypeHistogram {
		t.Fatalf("request header size = %+v, want histogram 12 By", m)
	}
	if m := only(t, exp.Metrics(), "http.client.request.header.count"); m.Value != 1 || m.Unit != "{header}" {
		t.Fatalf("request header count = %+v, want 1", m)
	}
	if m := only(t, exp.Metrics(), "http.client.response.header.size"); m.Value != float64(want) || want == 0 {
		t.Fatalf("response header size = %v, want %d", m.Value, want)
	}
	if m := only(t, exp.Metrics(), "http.client.response.header.count"); m.Value != float64(wantCount) {
		t.Fatalf("response header count = %v, want %d", m.Value, wantCount)
	}
	wantString(t, only(t, exp.Metrics(), "http.client.response.header.size"), "http.response.status_code", "200")
}

func TestClientNoRequestCountMetric(t *testing.T) {
	srv := newClientServer(t, okHandler(http.StatusOK, ""))
	hc, sc, exp := instrumented(t)
	resp, err := hc.Do(clientReq(t, http.MethodGet, srv.URL, nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	statstest.Flush(t, sc)
	for _, name := range []string{"http.client.request.count", "http.client.response.count"} {
		if got := byName(exp.Metrics(), name); len(got) != 0 {
			t.Fatalf("%s recorded; the duration histogram count already is the request count", name)
		}
	}
}

func TestClientErrorCounterRoundTrip(t *testing.T) {
	sc, exp := statstest.NewClient(t)
	tr := httpstats.NewTransportWith(sc, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("boom")
	}))
	resp, err := tr.RoundTrip(clientReq(t, http.MethodGet, "http://example.com", nil))
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("want an error")
	}
	statstest.Flush(t, sc)

	e := only(t, exp.Metrics(), "http.client.error.count")
	if e.Type != models.MetricTypeCounter || e.Value != 1 || e.Unit != "{error}" {
		t.Fatalf("error counter = %+v, want counter 1 {error}", e)
	}
	wantString(t, e, "error.type", "*errors.errorString")
	// A failed round trip has no response headers.
	if got := byName(exp.Metrics(), "http.client.response.header.size"); len(got) != 0 {
		t.Fatalf("response header size recorded after a failed round trip")
	}
	only(t, exp.Metrics(), "http.client.request.header.size")
}

func TestClientErrorCounterBodyAndStatus(t *testing.T) {
	sc, exp := statstest.NewClient(t)
	tr := httpstats.NewTransportWith(sc, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: &failingBody{data: "abc", err: errors.New("reset")}}, nil
	}))
	resp, err := tr.RoundTrip(clientReq(t, http.MethodGet, "http://example.com", nil))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	statstest.Flush(t, sc)
	only(t, exp.Metrics(), "http.client.error.count")

	srv := newClientServer(t, okHandler(http.StatusBadGateway, ""))
	hc, sc2, exp2 := instrumented(t)
	r2, err := hc.Do(clientReq(t, http.MethodGet, srv.URL, nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = r2.Body.Close()
	statstest.Flush(t, sc2)
	wantString(t, only(t, exp2.Metrics(), "http.client.error.count"), "error.type", "502")

	srv = newClientServer(t, okHandler(http.StatusNotFound, ""))
	hc, sc3, exp3 := instrumented(t)
	r3, err := hc.Do(clientReq(t, http.MethodGet, srv.URL, nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = r3.Body.Close()
	statstest.Flush(t, sc3)
	if got := byName(exp3.Metrics(), "http.client.error.count"); len(got) != 0 {
		t.Fatalf("404 counted as an error")
	}
}

func TestClientContentAttributesOptIn(t *testing.T) {
	srv := newClientServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = io.WriteString(w, "{}")
	}))
	run := func(opts ...httpstats.Option) []models.Metric {
		sc, exp := statstest.NewClient(t)
		hc := &http.Client{Transport: httpstats.NewTransportWith(sc, nil, opts...)}
		req := clientReq(t, http.MethodPost, srv.URL+"/secret-42", strings.NewReader("x"))
		req.Header.Set("Content-Type", "text/plain")
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		statstest.Flush(t, sc)
		return exp.Metrics()
	}

	wantAbsent(t, only(t, run(), clientDurationName), "http.request.header.content_type")

	ms := run(httpstats.WithContentAttributes())
	for _, name := range []string{clientDurationName, clientReqSizeName, "http.client.response.header.size"} {
		m := only(t, ms, name)
		wantString(t, m, "http.request.header.content_type", "text/plain")
		wantString(t, m, "http.response.header.content_type", "application/json")
		wantAbsent(t, m, "http.response.header.content_encoding")
	}
}
