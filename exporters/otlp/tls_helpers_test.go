package otlp

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testPKI is a throwaway CA with a server and a client certificate, written
// as PEM files in a temporary directory.
type testPKI struct {
	CAFile, ServerCertFile, ServerKeyFile, ClientCertFile, ClientKeyFile string
	CAPool                                                               *x509.CertPool
	ServerCert                                                           tls.Certificate
}

func newTestPKI(t *testing.T) *testPKI {
	t.Helper()
	dir := t.TempDir()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	caCert, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)

	leaf := func(serial int64, name string, usage x509.ExtKeyUsage) (certFile, keyFile string) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)
		template := &x509.Certificate{
			SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage},
			IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"localhost"},
		}
		der, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
		require.NoError(t, err)
		keyDER, err := x509.MarshalECPrivateKey(key)
		require.NoError(t, err)
		certFile = filepath.Join(dir, name+".pem")
		keyFile = filepath.Join(dir, name+".key")
		require.NoError(t, os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
		require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600))
		return certFile, keyFile
	}

	pki := &testPKI{CAFile: filepath.Join(dir, "ca.pem"), CAPool: x509.NewCertPool()}
	require.NoError(t, os.WriteFile(pki.CAFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600))
	pki.CAPool.AddCert(caCert)
	pki.ServerCertFile, pki.ServerKeyFile = leaf(2, "server", x509.ExtKeyUsageServerAuth)
	pki.ClientCertFile, pki.ClientKeyFile = leaf(3, "client", x509.ExtKeyUsageClientAuth)
	pki.ServerCert, err = tls.LoadX509KeyPair(pki.ServerCertFile, pki.ServerKeyFile)
	require.NoError(t, err)
	return pki
}

// serverTLS returns a server config that requires a client certificate signed
// by the PKI's CA when mutual is true.
func (p *testPKI) serverTLS(mutual bool) *tls.Config {
	cfg := &tls.Config{Certificates: []tls.Certificate{p.ServerCert}, MinVersion: tls.VersionTLS12}
	if mutual {
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
		cfg.ClientCAs = p.CAPool
	}
	return cfg
}
