package otlp

import (
	"context"
	"crypto/tls"
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
	"google.golang.org/grpc/credentials"

	"github.com/convoy-road-trips-app/stats/models"
)

type tlsCollector struct {
	URL      string
	Endpoint string
	requests chan *http.Request
}

func startTLSHTTPCollector(t *testing.T, serverCfg *tls.Config) *tlsCollector {
	t.Helper()
	c := &tlsCollector{requests: make(chan *http.Request, 8)}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.requests <- r
	}))
	server.TLS = serverCfg
	server.StartTLS()
	t.Cleanup(server.Close)
	c.URL = server.URL
	c.Endpoint = server.Listener.Addr().String()
	return c
}

func (c *tlsCollector) received(t *testing.T) *http.Request {
	t.Helper()
	select {
	case r := <-c.requests:
		return r
	case <-time.After(5 * time.Second):
		require.FailNow(t, "no export reached the collector")
		return nil
	}
}

func startTLSGRPCCollector(t *testing.T, serverCfg *tls.Config) (collector *grpcCollector, endpoint string) {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	collector = &grpcCollector{exports: make(chan struct{}, 8)}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(serverCfg)))
	collectormetricspb.RegisterMetricsServiceServer(server, collector)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return collector, listener.Addr().String()
}

func awaitGRPCExport(t *testing.T, c *grpcCollector) {
	t.Helper()
	select {
	case <-c.exports:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "no export reached the collector")
	}
}

func exportOneErr(config *models.OTLPConfig) error {
	config.Enabled = true
	exporter, err := NewExporter(config)
	if err != nil {
		return err
	}
	defer func() { _ = exporter.Shutdown(context.Background()) }()
	return exporter.Export(context.Background(), []*models.Metric{
		{Name: "requests_total", Type: models.MetricTypeCounter, Value: 1, Timestamp: time.Now()},
	})
}

func TestHTTPCAFileTrustsPrivateServer(t *testing.T) {
	pki := newTestPKI(t)
	collector := startTLSHTTPCollector(t, pki.serverTLS(false))

	require.NoError(t, exportOneErr(&models.OTLPConfig{Endpoint: collector.URL, Protocol: models.OTLPProtocolHTTP, CAFile: pki.CAFile}))
	require.Equal(t, "/v1/metrics", collector.received(t).URL.Path)
}

func TestHTTPDefaultRootsRejectPrivateServer(t *testing.T) {
	pki := newTestPKI(t)
	collector := startTLSHTTPCollector(t, pki.serverTLS(false))

	err := exportOneErr(&models.OTLPConfig{Endpoint: collector.URL, Protocol: models.OTLPProtocolHTTP})
	require.Error(t, err, "an unknown CA must not be trusted by default")
}

func TestHTTPMutualTLSWithFiles(t *testing.T) {
	pki := newTestPKI(t)
	collector := startTLSHTTPCollector(t, pki.serverTLS(true))

	require.NoError(t, exportOneErr(&models.OTLPConfig{
		Endpoint: collector.URL, Protocol: models.OTLPProtocolHTTP, CAFile: pki.CAFile,
		ClientCertFile: pki.ClientCertFile, ClientKeyFile: pki.ClientKeyFile,
	}))
	request := collector.received(t)
	require.Len(t, request.TLS.PeerCertificates, 1)
	require.Equal(t, "client", request.TLS.PeerCertificates[0].Subject.CommonName)
}

func TestHTTPMutualTLSRequiresClientCertificate(t *testing.T) {
	pki := newTestPKI(t)
	collector := startTLSHTTPCollector(t, pki.serverTLS(true))

	err := exportOneErr(&models.OTLPConfig{Endpoint: collector.URL, Protocol: models.OTLPProtocolHTTP, CAFile: pki.CAFile})
	require.Error(t, err)
}

func TestHTTPCustomTLSConfig(t *testing.T) {
	pki := newTestPKI(t)
	clientCert, err := tls.LoadX509KeyPair(pki.ClientCertFile, pki.ClientKeyFile)
	require.NoError(t, err)
	collector := startTLSHTTPCollector(t, pki.serverTLS(true))

	require.NoError(t, exportOneErr(&models.OTLPConfig{
		Endpoint: collector.URL, Protocol: models.OTLPProtocolHTTP,
		TLSConfig: &tls.Config{RootCAs: pki.CAPool, Certificates: []tls.Certificate{clientCert}},
	}))
	collector.received(t)
}

func TestHTTPCAFileOverridesTLSConfigRoots(t *testing.T) {
	pki := newTestPKI(t)
	collector := startTLSHTTPCollector(t, pki.serverTLS(false))

	require.NoError(t, exportOneErr(&models.OTLPConfig{
		Endpoint: collector.URL, Protocol: models.OTLPProtocolHTTP, CAFile: pki.CAFile,
		TLSConfig: &tls.Config{ServerName: "localhost"},
	}))
	collector.received(t)
}

func TestGRPCCAFileTrustsPrivateServer(t *testing.T) {
	pki := newTestPKI(t)
	collector, endpoint := startTLSGRPCCollector(t, pki.serverTLS(false))

	require.NoError(t, exportOneErr(&models.OTLPConfig{Endpoint: endpoint, Protocol: models.OTLPProtocolGRPC, CAFile: pki.CAFile}))
	awaitGRPCExport(t, collector)
}

func TestGRPCMutualTLSWithFiles(t *testing.T) {
	pki := newTestPKI(t)
	collector, endpoint := startTLSGRPCCollector(t, pki.serverTLS(true))

	require.NoError(t, exportOneErr(&models.OTLPConfig{
		Endpoint: endpoint, Protocol: models.OTLPProtocolGRPC, CAFile: pki.CAFile,
		ClientCertFile: pki.ClientCertFile, ClientKeyFile: pki.ClientKeyFile,
	}))
	awaitGRPCExport(t, collector)
}

func TestGRPCMutualTLSRequiresClientCertificate(t *testing.T) {
	pki := newTestPKI(t)
	_, endpoint := startTLSGRPCCollector(t, pki.serverTLS(true))

	require.Error(t, exportOneErr(&models.OTLPConfig{Endpoint: endpoint, Protocol: models.OTLPProtocolGRPC, CAFile: pki.CAFile}))
}

func TestGRPCCustomTLSConfig(t *testing.T) {
	pki := newTestPKI(t)
	collector, endpoint := startTLSGRPCCollector(t, pki.serverTLS(false))

	require.NoError(t, exportOneErr(&models.OTLPConfig{
		Endpoint: endpoint, Protocol: models.OTLPProtocolGRPC, TLSConfig: &tls.Config{RootCAs: pki.CAPool},
	}))
	awaitGRPCExport(t, collector)
}

func TestTLSFilesIgnoredForInsecure(t *testing.T) {
	collector := startHTTPCollector(t)

	require.NoError(t, exportOneErr(&models.OTLPConfig{
		Endpoint: collector.URL, Protocol: models.OTLPProtocolHTTP, Insecure: true,
		CAFile: "/nonexistent/ca.pem", ClientCertFile: "/nonexistent/c.pem", ClientKeyFile: "/nonexistent/k.pem",
	}))
	collector.next(t)
}

func TestTLSFileErrorsFailConstruction(t *testing.T) {
	pki := newTestPKI(t)
	notPEM := filepath.Join(t.TempDir(), "junk.pem")
	require.NoError(t, os.WriteFile(notPEM, []byte("not pem"), 0o600))
	for name, config := range map[string]*models.OTLPConfig{
		"missing CA":     {CAFile: "/nonexistent/ca.pem"},
		"CA without PEM": {CAFile: notPEM},
		"missing pair":   {ClientCertFile: "/nonexistent/c.pem", ClientKeyFile: "/nonexistent/k.pem"},
		"key mismatch":   {ClientCertFile: pki.ClientCertFile, ClientKeyFile: pki.ServerKeyFile},
	} {
		for _, protocol := range []models.OTLPProtocol{models.OTLPProtocolHTTP, models.OTLPProtocolGRPC} {
			t.Run(name+"/"+string(protocol), func(t *testing.T) {
				config.Enabled, config.Endpoint, config.Protocol = true, "localhost:4318", protocol
				_, err := NewExporter(config)
				require.Error(t, err)
			})
		}
	}
}

func TestClientCertWithoutKeyRejected(t *testing.T) {
	_, err := NewExporter(&models.OTLPConfig{Enabled: true, Endpoint: "localhost:4318", ClientCertFile: "c.pem"})
	require.ErrorContains(t, err, "together")
}

func TestOnlyExplicitTLSIsUsedNotEnv(t *testing.T) {
	pki := newTestPKI(t)
	t.Setenv("OTEL_EXPORTER_OTLP_CERTIFICATE", "/nonexistent")
	t.Setenv("OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE", "/nonexistent")
	t.Setenv("OTEL_EXPORTER_OTLP_CLIENT_KEY", "/nonexistent")
	collector := startTLSHTTPCollector(t, pki.serverTLS(false))

	require.NoError(t, exportOneErr(&models.OTLPConfig{Endpoint: collector.URL, Protocol: models.OTLPProtocolHTTP, CAFile: pki.CAFile}))
	collector.received(t)
}
