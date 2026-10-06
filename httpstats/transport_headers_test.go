package httpstats_test

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/convoy-road-trips-app/stats/httpstats"
	"github.com/convoy-road-trips-app/stats/statstest"
)

// TestClientHeadersNotRetained checks that header sizes and counts are taken
// when the round trip returns, so later mutations do not change them.
func TestClientHeadersNotRetained(t *testing.T) {
	sc, exp := statstest.NewClient(t)
	tr := httpstats.NewTransportWith(sc, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"X-A": {"1"}},
			Body:       io.NopCloser(strings.NewReader("hello")),
		}, nil
	}))
	req := clientReq(t, http.MethodGet, "http://example.com", nil)
	req.Header.Set("X-One", "abc")
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}

	// Mutate both header maps after RoundTrip returned, before the body ends.
	req.Header.Set("X-Added", "a-long-value-that-changes-the-size")
	resp.Header.Set("X-Added", "a-long-value-that-changes-the-size")
	resp.Header.Del("X-A")

	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	statstest.Flush(t, sc)

	// "X-One: abc\r\n" is 12 bytes and "X-A: 1\r\n" is 8 bytes.
	for name, want := range map[string]float64{
		"http.client.request.header.size":   12,
		"http.client.request.header.count":  1,
		"http.client.response.header.size":  8,
		"http.client.response.header.count": 1,
	} {
		if m := only(t, exp.Metrics(), name); m.Value != want {
			t.Errorf("%s = %v, want %v", name, m.Value, want)
		}
	}
}

// TestClientHeaderMutationRace mutates the response and request headers while
// another goroutine drains and closes the body; run with -race.
func TestClientHeaderMutationRace(t *testing.T) {
	srv := newClientServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-A", "1")
		_, _ = w.Write([]byte(strings.Repeat("x", 1<<16)))
	}))
	hc, sc, _ := instrumented(t)
	for range 20 {
		drainWhileMutating(t, hc, srv.URL)
	}
	statstest.Flush(t, sc)
}

// drainWhileMutating does one request, draining and closing its body while
// another goroutine mutates the request and response headers.
func drainWhileMutating(t *testing.T, hc *http.Client, target string) {
	t.Helper()
	req := clientReq(t, http.MethodGet, target, nil)
	req.Header.Set("X-One", "abc")
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 200 {
			resp.Header.Set("X-Mut", strings.Repeat("v", i))
			req.Header.Set("X-Mut", strings.Repeat("v", i))
		}
	}()
	_, _ = io.Copy(io.Discard, resp.Body)
	wg.Wait()
}
