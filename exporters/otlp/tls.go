package otlp

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"slices"

	"github.com/convoy-road-trips-app/stats/models"
)

// buildTLSConfig returns the TLS configuration of a secure connection: a clone
// of config.TLSConfig (or an empty one), a TLS 1.2 floor when none is set, the
// CA roots from CAFile and the client key pair from ClientCertFile and
// ClientKeyFile. The files are read here, once, so the SDK never consults
// OTEL_EXPORTER_OTLP_CERTIFICATE or the CLIENT_* variables itself; the root
// package resolves those variables into the config fields. Verification is
// never relaxed: InsecureSkipVerify stays false unless the caller set it in
// config.TLSConfig.
func buildTLSConfig(config *models.OTLPConfig) (*tls.Config, error) {
	cfg := config.TLSConfig.Clone()
	if cfg == nil {
		cfg = &tls.Config{}
	}
	if cfg.MinVersion == 0 {
		cfg.MinVersion = tls.VersionTLS12
	}
	if config.CAFile != "" {
		pem, err := os.ReadFile(config.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read otlp CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("otlp CA file %q contains no PEM certificate", config.CAFile)
		}
		cfg.RootCAs = pool
	}
	if config.ClientCertFile != "" || config.ClientKeyFile != "" {
		pair, err := tls.LoadX509KeyPair(config.ClientCertFile, config.ClientKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load otlp client certificate: %w", err)
		}
		cfg.Certificates = append(slices.Clone(cfg.Certificates), pair)
	}
	return cfg, nil
}
