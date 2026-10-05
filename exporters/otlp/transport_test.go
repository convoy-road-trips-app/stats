package otlp

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/grpc"

	"github.com/convoy-road-trips-app/stats/models"
)

// writeSelfSignedPEM writes a valid, self-signed certificate and returns its path.
func writeSelfSignedPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "env-cert"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	return path
}

// httpCollector records every request an OTLP/HTTP exporter sends.
type httpCollector struct {
	URL      string
	requests chan *http.Request
	bodies   chan []byte
}

func startHTTPCollector(t *testing.T) *httpCollector {
	t.Helper()
	c := &httpCollector{requests: make(chan *http.Request, 8), bodies: make(chan []byte, 8)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		c.requests <- r
		c.bodies <- body
	}))
	t.Cleanup(server.Close)
	c.URL = server.URL
	return c
}

func (c *httpCollector) next(t *testing.T) (request *http.Request, body []byte) {
	t.Helper()
	select {
	case r := <-c.requests:
		return r, <-c.bodies
	case <-time.After(5 * time.Second):
		require.FailNow(t, "no export reached the collector")
		return nil, nil
	}
}

// grpcCollector counts the exports an OTLP/gRPC exporter delivers.
type grpcCollector struct {
	collectormetricspb.UnimplementedMetricsServiceServer
	exports chan struct{}
}

func (c *grpcCollector) Export(context.Context, *collectormetricspb.ExportMetricsServiceRequest) (*collectormetricspb.ExportMetricsServiceResponse, error) {
	c.exports <- struct{}{}
	return &collectormetricspb.ExportMetricsServiceResponse{}, nil
}

func startPlaintextGRPCCollector(t *testing.T) (collector *grpcCollector, endpoint string) {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	collector = &grpcCollector{exports: make(chan struct{}, 8)}
	server := grpc.NewServer()
	collectormetricspb.RegisterMetricsServiceServer(server, collector)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return collector, listener.Addr().String()
}

func exportOne(t *testing.T, config *models.OTLPConfig) {
	t.Helper()
	config.Enabled = true
	exporter, err := NewExporter(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, exporter.Shutdown(context.Background())) })
	require.NoError(t, exporter.Export(context.Background(), []*models.Metric{
		{Name: "requests_total", Type: models.MetricTypeCounter, Value: 1, Timestamp: time.Now()},
	}))
}

func TestEnvCertificateIgnoredForInsecureHTTP(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_CERTIFICATE", writeSelfSignedPEM(t))
	collector := startHTTPCollector(t)

	exportOne(t, &models.OTLPConfig{Endpoint: collector.URL, Insecure: true, Protocol: models.OTLPProtocolHTTP})

	request, _ := collector.next(t)
	require.Equal(t, "/v1/metrics", request.URL.Path)
}

func TestEnvCertificateIgnoredForInsecureGRPC(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_CERTIFICATE", writeSelfSignedPEM(t))
	collector, endpoint := startPlaintextGRPCCollector(t)

	exportOne(t, &models.OTLPConfig{Endpoint: endpoint, Insecure: true, Protocol: models.OTLPProtocolGRPC})

	select {
	case <-collector.exports:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "no export reached the collector")
	}
}

func TestUnrelatedEnvCannotChangeTransport(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "http://elsewhere.invalid:1/other/path")
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "false")
	t.Setenv("OTEL_EXPORTER_OTLP_CERTIFICATE", "/nonexistent")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "x-env=leaked")
	t.Setenv("OTEL_EXPORTER_OTLP_COMPRESSION", "gzip")
	collector := startHTTPCollector(t)

	exportOne(t, &models.OTLPConfig{Endpoint: collector.URL, Insecure: true, Protocol: models.OTLPProtocolHTTP})

	request, _ := collector.next(t)
	require.Equal(t, "/v1/metrics", request.URL.Path)
	require.Empty(t, request.Header.Get("x-env"), "environment headers must not leak into explicit config")
	require.Empty(t, request.Header.Get("Content-Encoding"), "environment compression must not apply")
}

func TestExplicitHeadersAndGzipReachHTTPCollector(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_COMPRESSION", "none")
	collector := startHTTPCollector(t)

	exportOne(t, &models.OTLPConfig{
		Endpoint: collector.URL + "/custom/metrics", Insecure: true, Protocol: models.OTLPProtocolHTTP,
		Headers: map[string]string{"x-token": "abc"}, Compression: "gzip",
	})

	request, body := collector.next(t)
	require.Equal(t, "/custom/metrics", request.URL.Path)
	require.Equal(t, "abc", request.Header.Get("x-token"))
	require.Equal(t, "gzip", request.Header.Get("Content-Encoding"))
	reader, err := gzip.NewReader(bytes.NewReader(body))
	require.NoError(t, err)
	_, err = io.ReadAll(reader)
	require.NoError(t, err)
}

func TestHTTPSchemeMakesEndpointInsecureAndHostPortGetsDefaultPath(t *testing.T) {
	collector := startHTTPCollector(t)
	hostPort := collector.URL[len("http://"):]

	exportOne(t, &models.OTLPConfig{Endpoint: hostPort, Insecure: true, Protocol: models.OTLPProtocolHTTP})

	request, _ := collector.next(t)
	require.Equal(t, "/v1/metrics", request.URL.Path)
}

func TestInvalidEndpointSchemeRejected(t *testing.T) {
	_, err := NewExporter(&models.OTLPConfig{Enabled: true, Endpoint: "ftp://host:21", Protocol: models.OTLPProtocolHTTP})
	require.Error(t, err)
}
