package httpstats_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/convoy-road-trips-app/stats"
	"github.com/convoy-road-trips-app/stats/httpstats"
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

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
