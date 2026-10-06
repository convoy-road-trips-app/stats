package httpstats_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/convoy-road-trips-app/stats/httpstats"
	"github.com/convoy-road-trips-app/stats/statstest"
)

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
