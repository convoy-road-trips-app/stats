package otlp

import (
	"context"
	"crypto/tls"
	"fmt"
	"maps"
	"net/url"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"google.golang.org/grpc/credentials"

	"github.com/convoy-road-trips-app/stats/models"
)

// Transport guarantee: the effective endpoint, path, TLS mode, headers,
// timeout and compression of the SDK exporters are always those resolved by
// this library. The vendored constructors read OTEL_EXPORTER_OTLP_* variables
// before our options run, so every setting is passed explicitly below and any
// TLS derived from the environment is cleared. The certificate variables
// (OTEL_EXPORTER_OTLP_CERTIFICATE, CLIENT_CERTIFICATE, CLIENT_KEY) are resolved
// by the root package into OTLPConfig fields and read by buildTLSConfig, so the
// SDK never reads them itself.

const (
	defaultExportTimeout = 10 * time.Second
	defaultMetricsPath   = "/v1/metrics"
)

func newTransport(config *models.OTLPConfig) (otlpMetricExporter, error) {
	switch config.Protocol {
	case models.OTLPProtocolHTTP:
		return newHTTPExporter(config)
	default:
		return newGRPCExporter(config)
	}
}

// endpointSettings is the endpoint split into what the SDK needs.
type endpointSettings struct {
	host     string
	path     string // URL path; empty selects the OTLP default for HTTP
	insecure bool
}

// parseEndpoint accepts "host:port" or an http(s) URL. Insecure is true when
// the config says so or the URL scheme is http, as the SDK's own
// WithEndpointURL treats it.
func parseEndpoint(config *models.OTLPConfig) (endpointSettings, error) {
	raw := strings.TrimSpace(config.Endpoint)
	settings := endpointSettings{host: raw, insecure: config.Insecure}
	if !strings.Contains(raw, "://") {
		return settings, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return endpointSettings{}, fmt.Errorf("invalid otlp endpoint %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" || u.Host == "" {
		return endpointSettings{}, fmt.Errorf("invalid otlp endpoint %q: want host:port or an http(s) URL", raw)
	}
	return endpointSettings{host: u.Host, path: u.Path, insecure: config.Insecure || u.Scheme == "http"}, nil
}

// url returns the URL passed to the SDK's WithEndpointURL. Its scheme encodes
// the resolved TLS mode, so the SDK derives the same mode we pass explicitly.
func (e endpointSettings) url(path string) string {
	scheme := "https"
	if e.insecure {
		scheme = "http"
	}
	return (&url.URL{Scheme: scheme, Host: e.host, Path: path}).String()
}

func exportTimeout(config *models.OTLPConfig) time.Duration {
	if config.ExportTimeout == 0 {
		return defaultExportTimeout
	}
	return config.ExportTimeout
}

// headersOrEmpty returns a copy that is never nil, so that no header from the
// environment can survive when none are configured.
func headersOrEmpty(headers map[string]string) map[string]string {
	out := make(map[string]string, len(headers))
	maps.Copy(out, headers)
	return out
}

func newGRPCExporter(config *models.OTLPConfig) (*otlpmetricgrpc.Exporter, error) {
	endpoint, err := parseEndpoint(config)
	if err != nil {
		return nil, err
	}
	var tlsConfig *tls.Config // an insecure endpoint ignores, and never reads, TLS settings
	if !endpoint.insecure {
		if tlsConfig, err = buildTLSConfig(config); err != nil {
			return nil, err
		}
	}
	opts := []otlpmetricgrpc.Option{
		otlpmetricgrpc.WithEndpointURL(endpoint.url("")),
		otlpmetricgrpc.WithHeaders(headersOrEmpty(config.Headers)),
		otlpmetricgrpc.WithTimeout(exportTimeout(config)),
	}
	if compressor, ok := grpcCompressor(config.Compression); ok {
		opts = append(opts, otlpmetricgrpc.WithCompressor(compressor))
	}
	if endpoint.insecure {
		// Credentials from the environment would win over WithInsecure
		// (oconf/options.go:150-154), so clear them first.
		opts = append(opts, otlpmetricgrpc.WithTLSCredentials(nil), otlpmetricgrpc.WithInsecure())
	} else {
		opts = append(opts, otlpmetricgrpc.WithTLSCredentials(credentials.NewTLS(tlsConfig)))
	}
	if len(config.GRPCDialOptions) > 0 {
		opts = append(opts, otlpmetricgrpc.WithDialOption(config.GRPCDialOptions...))
	}
	if r := config.Retry; r != nil {
		opts = append(opts, otlpmetricgrpc.WithRetry(otlpmetricgrpc.RetryConfig{
			Enabled: true, InitialInterval: r.InitialInterval, MaxInterval: r.MaxInterval, MaxElapsedTime: r.MaxElapsedTime,
		}))
	}

	exp, err := otlpmetricgrpc.New(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("create otlp grpc exporter: %w", err)
	}
	return exp, nil
}

// grpcCompressor returns the compressor to pass and whether to pass one.
//
// The SDK maps "gzip" to gzip and anything else to no compression
// (otlpmetricgrpc/config.go:116-123, compressorToCompression), so "" clears a
// gzip setting that came from the environment. But it also reports every
// non-"gzip" value through otel.Handle, which logs noise on each construction,
// so "" is passed only when one of the variables the SDK reads for compression
// is set (oconf/envconfig.go:97-98); otherwise the SDK default is already none.
func grpcCompressor(compression string) (compressor string, pass bool) {
	if compression == "gzip" {
		return "gzip", true
	}
	for _, name := range []string{"OTEL_EXPORTER_OTLP_COMPRESSION", "OTEL_EXPORTER_OTLP_METRICS_COMPRESSION"} {
		if _, set := os.LookupEnv(name); set {
			return "", true
		}
	}
	return "", false
}

func newHTTPExporter(config *models.OTLPConfig) (*otlpmetrichttp.Exporter, error) {
	endpoint, err := parseEndpoint(config)
	if err != nil {
		return nil, err
	}
	var tlsConfig *tls.Config // an insecure endpoint ignores, and never reads, TLS settings
	if !endpoint.insecure {
		if tlsConfig, err = buildTLSConfig(config); err != nil {
			return nil, err
		}
	}
	path := endpoint.path
	if path == "" {
		path = defaultMetricsPath
	}
	compression := otlpmetrichttp.NoCompression
	if config.Compression == "gzip" {
		compression = otlpmetrichttp.GzipCompression
	}
	opts := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpointURL(endpoint.url(path)),
		otlpmetrichttp.WithHeaders(headersOrEmpty(config.Headers)),
		otlpmetrichttp.WithTimeout(exportTimeout(config)),
		otlpmetrichttp.WithCompression(compression),
	}
	if endpoint.insecure {
		// A nil config clears TLS from the environment; the SDK refuses an
		// insecure endpoint that still has TLS configured
		// (otlpmetrichttp/client.go:71-72). tls.Config.Clone(nil) is nil.
		opts = append(opts, otlpmetrichttp.WithTLSClientConfig(nil))
	} else {
		opts = append(opts, otlpmetrichttp.WithTLSClientConfig(tlsConfig))
	}
	if config.HTTPClient != nil {
		opts = append(opts, otlpmetrichttp.WithHTTPClient(config.HTTPClient))
	}
	if r := config.Retry; r != nil {
		opts = append(opts, otlpmetrichttp.WithRetry(otlpmetrichttp.RetryConfig{
			Enabled: true, InitialInterval: r.InitialInterval, MaxInterval: r.MaxInterval, MaxElapsedTime: r.MaxElapsedTime,
		}))
	}

	exp, err := otlpmetrichttp.New(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("create otlp http exporter: %w", err)
	}
	return exp, nil
}
