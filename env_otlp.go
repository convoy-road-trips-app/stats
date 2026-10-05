package stats

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/convoy-road-trips-app/stats/models"
)

const (
	envServiceName      = "OTEL_SERVICE_NAME"
	envExportInterval   = "OTEL_METRIC_EXPORT_INTERVAL"
	envTemporality      = "OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE"
	envGenericEndpoint  = "OTEL_EXPORTER_OTLP_ENDPOINT"
	envMetricsPrefix    = "OTEL_EXPORTER_OTLP_METRICS_"
	envGenericPrefix    = "OTEL_EXPORTER_OTLP_"
	metricsEndpointPath = "/v1/metrics"
)

// buildConfig applies the defaults, the OTEL_SERVICE_NAME default and the
// options, then fills the OTLP settings the options left open from the
// environment. The environment never enables OTLP by itself.
func buildConfig(opts []Option) (*Config, error) {
	cfg := DefaultConfig()
	if name := envValue(envServiceName); name != "" {
		cfg.ServiceName = name
	}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.OTLP != nil && cfg.OTLP.Enabled {
		if err := resolveOTLPEnv(cfg); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

// resolveOTLPEnv fills every OTLP setting without an override from the
// environment, METRICS_ variables before generic ones. Settings stated through
// options are not read from the environment, so an invalid variable cannot
// break a caller that overrode it.
func resolveOTLPEnv(cfg *Config) error {
	// Protocol goes first: the generic endpoint depends on it.
	for _, resolve := range []func(*Config) error{
		resolveProtocol, resolveEndpoint, resolveInsecure, resolveHeaders,
		resolveTimeout, resolveCompression, resolveTemporality, resolveExportInterval,
	} {
		if err := resolve(cfg); err != nil {
			return err
		}
	}
	return nil
}

type envSetting struct{ name, value string }

func envValue(name string) string { return strings.TrimSpace(os.Getenv(name)) }

// signalEnv returns the OTEL_EXPORTER_OTLP_METRICS_<suffix> variable if set,
// otherwise OTEL_EXPORTER_OTLP_<suffix>. Empty values count as unset.
func signalEnv(suffix string) (envSetting, bool) {
	for _, prefix := range []string{envMetricsPrefix, envGenericPrefix} {
		if value := envValue(prefix + suffix); value != "" {
			return envSetting{prefix + suffix, value}, true
		}
	}
	return envSetting{}, false
}

func (s envSetting) errorf(format string, args ...any) error {
	return fmt.Errorf("%w: %s=%q: %s", ErrInvalidConfig, s.name, s.value, fmt.Sprintf(format, args...))
}

func resolveProtocol(cfg *Config) error {
	if cfg.OTLPOverrides.Protocol != nil {
		return nil
	}
	setting, ok := signalEnv("PROTOCOL")
	if !ok {
		return nil
	}
	switch setting.value {
	case "grpc":
		cfg.OTLP.Protocol = models.OTLPProtocolGRPC
	case "http/protobuf":
		cfg.OTLP.Protocol = models.OTLPProtocolHTTP
	case "http/json":
		return setting.errorf("http/json is not supported (use grpc or http/protobuf)")
	default:
		return setting.errorf("unknown protocol (use grpc or http/protobuf)")
	}
	return nil
}

// resolveEndpoint uses the signal-specific endpoint as-is; the generic one
// gets /v1/metrics appended when the protocol is HTTP.
func resolveEndpoint(cfg *Config) error {
	if cfg.OTLPOverrides.Endpoint != nil {
		return nil
	}
	setting, ok := signalEnv("ENDPOINT")
	if !ok {
		return nil
	}
	endpoint := setting.value
	if setting.name == envGenericEndpoint && cfg.OTLP.Protocol == models.OTLPProtocolHTTP {
		endpoint = strings.TrimRight(endpoint, "/") + metricsEndpointPath
	}
	cfg.OTLP.Endpoint = endpoint
	return nil
}

func resolveInsecure(cfg *Config) error {
	if cfg.OTLPOverrides.Insecure != nil {
		return nil
	}
	setting, ok := signalEnv("INSECURE")
	if !ok {
		return nil
	}
	switch strings.ToLower(setting.value) {
	case "true":
		cfg.OTLP.Insecure = true
	case "false":
		cfg.OTLP.Insecure = false
	default:
		return setting.errorf("must be true or false")
	}
	return nil
}

func resolveHeaders(cfg *Config) error {
	if cfg.OTLPOverrides.Headers != nil {
		return nil
	}
	setting, ok := signalEnv("HEADERS")
	if !ok {
		return nil
	}
	headers, err := parseHeaders(setting.value)
	if err != nil {
		return setting.errorf("%v", err)
	}
	cfg.OTLP.Headers = headers
	return nil
}

// parseHeaders parses "k=v,k2=v2"; keys and values are percent-decoded.
func parseHeaders(value string) (map[string]string, error) {
	headers := make(map[string]string)
	for pair := range strings.SplitSeq(value, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		rawKey, rawValue, found := strings.Cut(pair, "=")
		if !found {
			return nil, fmt.Errorf("header %q is not key=value", pair)
		}
		key, err := url.PathUnescape(strings.TrimSpace(rawKey))
		if err != nil || key == "" {
			return nil, fmt.Errorf("invalid header key %q", rawKey)
		}
		decoded, err := url.PathUnescape(strings.TrimSpace(rawValue))
		if err != nil {
			return nil, fmt.Errorf("invalid header value for %q", key)
		}
		headers[key] = decoded
	}
	return headers, nil
}

func resolveTimeout(cfg *Config) error {
	if cfg.OTLPOverrides.Timeout != nil {
		return nil
	}
	setting, ok := signalEnv("TIMEOUT")
	if !ok {
		return nil
	}
	timeout, err := parseMilliseconds(setting.value)
	if err != nil {
		return setting.errorf("%v", err)
	}
	cfg.OTLP.ExportTimeout = timeout
	return nil
}

func resolveCompression(cfg *Config) error {
	if cfg.OTLPOverrides.Compression != nil {
		return nil
	}
	setting, ok := signalEnv("COMPRESSION")
	if !ok {
		return nil
	}
	switch strings.ToLower(setting.value) {
	case "gzip", "none":
		cfg.OTLP.Compression = strings.ToLower(setting.value)
		return nil
	default:
		return setting.errorf("must be gzip or none")
	}
}

func resolveTemporality(cfg *Config) error {
	if cfg.OTLPOverrides.Temporality != nil {
		return nil
	}
	value := envValue(envTemporality)
	if value == "" {
		return nil
	}
	setting := envSetting{envTemporality, value}
	switch strings.ToLower(value) {
	case "cumulative":
		cfg.OTLP.Temporality = models.Cumulative
	case "delta":
		cfg.OTLP.Temporality = models.Delta
	default:
		return setting.errorf("must be cumulative or delta")
	}
	return nil
}

// resolveExportInterval sets the pipeline flush interval from
// OTEL_METRIC_EXPORT_INTERVAL unless an option chose one.
func resolveExportInterval(cfg *Config) error {
	if cfg.FlushIntervalSet {
		return nil
	}
	value := envValue(envExportInterval)
	if value == "" {
		return nil
	}
	interval, err := parseMilliseconds(value)
	if err != nil {
		return envSetting{envExportInterval, value}.errorf("%v", err)
	}
	cfg.FlushInterval = interval
	return nil
}

// parseMilliseconds parses a positive integer number of milliseconds.
func parseMilliseconds(value string) (time.Duration, error) {
	ms, err := strconv.ParseInt(value, 10, 64)
	switch {
	case err != nil:
		return 0, errors.New("must be a whole number of milliseconds")
	case ms <= 0:
		return 0, errors.New("must be positive")
	case ms > math.MaxInt64/int64(time.Millisecond):
		return 0, errors.New("is too large")
	}
	return time.Duration(ms) * time.Millisecond, nil
}
