package otlp

import (
	"context"
	"crypto/tls"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/convoy-road-trips-app/stats/models"
)

type headerRoundTripper struct {
	next  http.RoundTripper
	calls atomic.Int32
}

func (h *headerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	h.calls.Add(1)
	r = r.Clone(r.Context())
	r.Header.Set("X-Custom-Client", "yes")
	return h.next.RoundTrip(r)
}

func TestHTTPClientEscapeHatchIsUsed(t *testing.T) {
	collector := startHTTPCollector(t)
	rt := &headerRoundTripper{next: http.DefaultTransport}

	exportOne(t, &models.OTLPConfig{
		Endpoint: collector.URL, Insecure: true, Protocol: models.OTLPProtocolHTTP,
		HTTPClient: &http.Client{Transport: rt},
	})

	request, _ := collector.next(t)
	require.Equal(t, "yes", request.Header.Get("X-Custom-Client"))
	require.Equal(t, int32(1), rt.calls.Load())
}

func TestHTTPClientUsedForSecureEndpointWithOwnTLS(t *testing.T) {
	pki := newTestPKI(t)
	collector := startTLSHTTPCollector(t, pki.serverTLS(false))
	rt := &headerRoundTripper{next: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pki.CAPool, MinVersion: tls.VersionTLS12}}}

	require.NoError(t, exportOneErr(&models.OTLPConfig{
		Endpoint: collector.URL, Protocol: models.OTLPProtocolHTTP, HTTPClient: &http.Client{Transport: rt},
	}))
	require.Equal(t, "yes", collector.received(t).Header.Get("X-Custom-Client"))
}

func TestHTTPClientSkipsTLSFilesItDoesNotOwn(t *testing.T) {
	pki := newTestPKI(t)
	collector := startTLSHTTPCollector(t, pki.serverTLS(false))
	rt := &headerRoundTripper{next: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pki.CAPool, MinVersion: tls.VersionTLS12}}}

	require.NoError(t, exportOneErr(&models.OTLPConfig{
		Endpoint: collector.URL, Protocol: models.OTLPProtocolHTTP, HTTPClient: &http.Client{Transport: rt},
		CAFile: "/nonexistent/ca.pem", ClientCertFile: "/nonexistent/client.pem", ClientKeyFile: "/nonexistent/client-key.pem",
	}))
	require.Equal(t, "yes", collector.received(t).Header.Get("X-Custom-Client"))
}

func TestGRPCDialOptionsEscapeHatchIsUsed(t *testing.T) {
	collector, endpoint := startPlaintextGRPCCollector(t)
	var calls atomic.Int32
	interceptor := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		calls.Add(1)
		return invoker(ctx, method, req, reply, cc, opts...)
	}

	exportOne(t, &models.OTLPConfig{
		Endpoint: endpoint, Insecure: true, Protocol: models.OTLPProtocolGRPC,
		GRPCDialOptions: []grpc.DialOption{grpc.WithChainUnaryInterceptor(interceptor)},
	})

	<-collector.exports
	require.Equal(t, int32(1), calls.Load())
}

func TestEscapeHatchesRequireMatchingProtocol(t *testing.T) {
	err := (&models.OTLPConfig{Enabled: true, Endpoint: "c:4317", Protocol: models.OTLPProtocolGRPC, HTTPClient: &http.Client{}}).Validate()
	require.ErrorContains(t, err, "HTTPClient")
	err = (&models.OTLPConfig{Enabled: true, Endpoint: "c:4318", Protocol: models.OTLPProtocolHTTP, GRPCDialOptions: []grpc.DialOption{grpc.WithUserAgent("x")}}).Validate()
	require.ErrorContains(t, err, "GRPCDialOptions")
}
