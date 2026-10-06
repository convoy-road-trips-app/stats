package stats

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// selfSigned writes a self-signed certificate valid for 127.0.0.1 that can act
// as server, client and CA, and returns its files and the parsed pair.
func selfSigned(t *testing.T) (certFile, keyFile string, pair tls.Certificate, pool *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "env-test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage:    x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	dir := t.TempDir()
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	require.NoError(t, os.WriteFile(certFile, certPEM, 0o600))
	require.NoError(t, os.WriteFile(keyFile, keyPEM, 0o600))
	pair, err = tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)
	pool = x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(certPEM))
	return certFile, keyFile, pair, pool
}

func TestEnvTLSFilesResolved(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_CERTIFICATE", "/generic/ca.pem")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_CERTIFICATE", "/metrics/ca.pem")
	t.Setenv("OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE", "/c.pem")
	t.Setenv("OTEL_EXPORTER_OTLP_CLIENT_KEY", "/k.pem")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "collector:4317")

	cfg := newConfigFrom(t, WithOTLPFromEnv())

	require.Equal(t, "/metrics/ca.pem", cfg.OTLP.CAFile)
	require.Equal(t, "/c.pem", cfg.OTLP.ClientCertFile)
	require.Equal(t, "/k.pem", cfg.OTLP.ClientKeyFile)
}

func TestExplicitTLSFilesBeatEnv(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_CERTIFICATE", "/env/ca.pem")
	t.Setenv("OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE", "/env/c.pem")
	t.Setenv("OTEL_EXPORTER_OTLP_CLIENT_KEY", "/env/k.pem")

	cfg := newConfigFrom(t, WithOTLP(&OTLPConfig{Endpoint: "c:4317", CAFile: "/opt/ca.pem"}))
	require.Equal(t, "/opt/ca.pem", cfg.OTLP.CAFile)
	require.Empty(t, cfg.OTLP.ClientCertFile, "a passed struct states every field, so env client cert is ignored")
	require.Empty(t, cfg.OTLP.ClientKeyFile)

	cfg = newConfigFrom(t, WithOTLPFromEnv(), WithOTLPCertificates("/opt/ca.pem", "", ""))
	require.Equal(t, "/opt/ca.pem", cfg.OTLP.CAFile)
	require.Equal(t, "/env/c.pem", cfg.OTLP.ClientCertFile, "unstated fields still come from env")
}

func TestWithOTLPCopiesTLSConfig(t *testing.T) {
	base := &tls.Config{ServerName: "a"}
	cfg := newConfigFrom(t, WithOTLP(&OTLPConfig{Endpoint: "c:4317", TLSConfig: base}))
	base.ServerName = "b"
	require.Equal(t, "a", cfg.OTLP.TLSConfig.ServerName)
}

func TestClientCertWithoutKeyFailsNewClient(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE", "/c.pem")
	_, err := NewClient(WithOTLP(&OTLPConfig{Endpoint: "c:4317"}), WithOTLPCertificates("", "/c.pem", ""))
	require.ErrorContains(t, err, "together")
}

// TestClient_OTLPEnvMutualTLSReachesCollector proves the certificate variables
// take effect on the wire through NewClient.
func TestClient_OTLPEnvMutualTLSReachesCollector(t *testing.T) {
	certFile, keyFile, pair, pool := selfSigned(t)
	received := make(chan *tls.ConnectionState, 4)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		received <- r.TLS
	}))
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{pair}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool,
		MinVersion: tls.VersionTLS12,
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", server.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_CERTIFICATE", certFile)
	t.Setenv("OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE", certFile)
	t.Setenv("OTEL_EXPORTER_OTLP_CLIENT_KEY", keyFile)

	client, err := NewClient(WithServiceName("svc"), WithOTLPFromEnv())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, client.Counter(ctx, "mtls.requests", 1))
	require.NoError(t, client.Flush(ctx))

	select {
	case state := <-received:
		require.Len(t, state.PeerCertificates, 1)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "no export reached the collector")
	}
	require.NoError(t, client.Shutdown(ctx))
}

func TestClient_OTLPTLSConfigOption(t *testing.T) {
	_, _, pair, pool := selfSigned(t)
	received := make(chan struct{}, 4)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { received <- struct{}{} }))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)

	client, err := NewClient(WithServiceName("svc"), WithOTLP(&OTLPConfig{
		Endpoint: server.URL, Protocol: OTLPProtocolHTTP, TLSConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13},
	}))
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, client.Counter(ctx, "tls.requests", 1))
	require.NoError(t, client.Flush(ctx))
	select {
	case <-received:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "no export reached the collector")
	}
	require.NoError(t, client.Shutdown(ctx))
}
