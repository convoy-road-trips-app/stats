package stats

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// newConfigFrom builds the configuration NewClient would validate, so tests
// can assert resolution without starting a pipeline.
func newConfigFrom(t *testing.T, opts ...Option) *Config {
	t.Helper()
	cfg, err := buildConfig(opts)
	require.NoError(t, err)
	return cfg
}

func TestEnvEndpointFilledWhenOTLPEnabled(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "collector:4317")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "api-key=s%20ecret,team=pay")
	t.Setenv("OTEL_EXPORTER_OTLP_TIMEOUT", "2500")
	t.Setenv("OTEL_EXPORTER_OTLP_COMPRESSION", "gzip")
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "delta")

	cfg := newConfigFrom(t, WithOTLPFromEnv())

	require.True(t, cfg.OTLP.Enabled)
	require.Equal(t, "collector:4317", cfg.OTLP.Endpoint)
	require.Equal(t, map[string]string{"api-key": "s ecret", "team": "pay"}, cfg.OTLP.Headers)
	require.Equal(t, 2500*time.Millisecond, cfg.OTLP.ExportTimeout)
	require.Equal(t, "gzip", cfg.OTLP.Compression)
	require.True(t, cfg.OTLP.Insecure)
	require.Equal(t, Delta, cfg.OTLP.Temporality)
}

func TestOptionBeatsEnv(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "a:4317")

	cfg := newConfigFrom(t, WithOTLP(&OTLPConfig{Endpoint: "b:4317"}))

	require.Equal(t, "b:4317", cfg.OTLP.Endpoint)
}

func TestMetricsSpecificBeatsGeneric(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "generic:4317")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "metrics:4317")
	t.Setenv("OTEL_EXPORTER_OTLP_TIMEOUT", "1000")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_TIMEOUT", "3000")

	cfg := newConfigFrom(t, WithOTLPFromEnv())

	require.Equal(t, "metrics:4317", cfg.OTLP.Endpoint)
	require.Equal(t, 3*time.Second, cfg.OTLP.ExportTimeout)
}

func TestGenericHTTPEndpointAppendsPath(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector:4318/")

	cfg := newConfigFrom(t, WithOTLPFromEnv())
	require.Equal(t, OTLPProtocolHTTP, cfg.OTLP.Protocol)
	require.Equal(t, "http://collector:4318/v1/metrics", cfg.OTLP.Endpoint)

	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "http://collector:4318/custom")
	cfg = newConfigFrom(t, WithOTLPFromEnv())
	require.Equal(t, "http://collector:4318/custom", cfg.OTLP.Endpoint)
}

func TestGenericGRPCEndpointKeptAsIs(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector:4317")

	cfg := newConfigFrom(t, WithOTLPFromEnv())

	require.Equal(t, OTLPProtocolGRPC, cfg.OTLP.Protocol)
	require.Equal(t, "http://collector:4317", cfg.OTLP.Endpoint)
}

func TestEnvAloneDoesNotEnableOTLP(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "collector:4317")
	t.Setenv("OTEL_EXPORTER_OTLP_TIMEOUT", "abc")

	cfg := newConfigFrom(t)

	require.Nil(t, cfg.OTLP)
}

func TestExplicitFalseInsecureBeatsEnvTrue(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "true")

	cfg := newConfigFrom(t, WithOTLP(&OTLPConfig{Endpoint: "c:4317", Insecure: false}))

	require.False(t, cfg.OTLP.Insecure)
}

func TestExplicitEmptyHeadersClearEnv(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "k=v")

	cfg := newConfigFrom(t, WithOTLP(&OTLPConfig{Endpoint: "c:4317"}))

	require.Empty(t, cfg.OTLP.Headers)
}

func TestOptionOrderWithOTLPThenDedicated(t *testing.T) {
	cfg := newConfigFrom(t,
		WithOTLP(&OTLPConfig{Endpoint: "c:4317", Temporality: Cumulative}),
		WithTemporality(Delta),
	)

	require.Equal(t, Delta, cfg.OTLP.Temporality)
}

func TestOptionOrderDedicatedThenWithOTLP(t *testing.T) {
	cfg := newConfigFrom(t,
		WithTemporality(Delta),
		WithOTLP(&OTLPConfig{Endpoint: "c:4317", Temporality: Cumulative}),
	)

	require.Equal(t, Cumulative, cfg.OTLP.Temporality)
}

func TestDedicatedOptionsBeatEnv(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "env:4317")
	t.Setenv("OTEL_EXPORTER_OTLP_TIMEOUT", "1000")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "delta")

	cfg := newConfigFrom(t,
		WithOTLPFromEnv(),
		WithOTLPExportTimeout(7*time.Second),
		WithTemporality(Cumulative),
	)

	require.Equal(t, "env:4317", cfg.OTLP.Endpoint)
	require.Equal(t, 7*time.Second, cfg.OTLP.ExportTimeout)
	require.Equal(t, Cumulative, cfg.OTLP.Temporality)
}

func TestWithOTLPStillCopies(t *testing.T) {
	buckets := []float64{1, 2, 3}
	headers := map[string]string{"k": "v"}
	attrs := OTLPConfig{Endpoint: "c:4317", HistogramBuckets: buckets, Headers: headers}
	option := WithOTLP(&attrs)

	cfg := DefaultConfig()
	option(cfg)
	attrs.Endpoint = "changed:4317"
	buckets[0] = 99
	headers["k"] = "changed"

	require.Equal(t, "c:4317", cfg.OTLP.Endpoint)
	require.Equal(t, []float64{1, 2, 3}, cfg.OTLP.HistogramBuckets)
	require.Equal(t, map[string]string{"k": "v"}, cfg.OTLP.Headers)
	require.NotNil(t, cfg.OTLPOverrides.Headers)
	require.Equal(t, map[string]string{"k": "v"}, *cfg.OTLPOverrides.Headers)
	require.Equal(t, "c:4317", *cfg.OTLPOverrides.Endpoint)
}

func TestEnvExportIntervalSetsFlushIntervalUnlessGiven(t *testing.T) {
	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "5000")

	cfg := newConfigFrom(t, WithOTLPFromEnv())
	require.Equal(t, 5*time.Second, cfg.FlushInterval)

	cfg = newConfigFrom(t, WithOTLPFromEnv(), WithFlushInterval(time.Hour))
	require.Equal(t, time.Hour, cfg.FlushInterval)

	cfg = newConfigFrom(t, WithOTLPFromEnv(), WithOTLPExportInterval(2*time.Second))
	require.Equal(t, 2*time.Second, cfg.FlushInterval)

	cfg = newConfigFrom(t, WithOTLP(&OTLPConfig{Endpoint: "c:4317"}), WithOTLPExportInterval(time.Second))
	require.Equal(t, time.Second, cfg.FlushInterval)
}

func TestEnvExportIntervalIgnoredWithoutOTLP(t *testing.T) {
	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "5000")

	cfg := newConfigFrom(t)

	require.Equal(t, DefaultConfig().FlushInterval, cfg.FlushInterval)
}

func TestEnvServiceNameIsDefaultForOptions(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "from-env")

	require.Equal(t, "from-env", newConfigFrom(t).ServiceName)
	require.Equal(t, "explicit", newConfigFrom(t, WithServiceName("explicit")).ServiceName)
}

func TestEnvProtocolValues(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "c:4317")

	tests := []struct {
		value   string
		want    OTLPProtocol
		wantErr bool
	}{
		{"grpc", OTLPProtocolGRPC, false},
		{"http/protobuf", OTLPProtocolHTTP, false},
		{"http/json", "", true},
		{"carrier-pigeon", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", tt.value)
			cfg, err := buildConfig([]Option{WithOTLPFromEnv()})
			if tt.wantErr {
				require.ErrorIs(t, err, ErrInvalidConfig)
				require.ErrorContains(t, err, "OTEL_EXPORTER_OTLP_PROTOCOL")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, cfg.OTLP.Protocol)
		})
	}
}

func TestInvalidEnvNamesTheVariable(t *testing.T) {
	tests := []struct{ name, value string }{
		{"OTEL_EXPORTER_OTLP_TIMEOUT", "abc"},
		{"OTEL_EXPORTER_OTLP_METRICS_TIMEOUT", "-5"},
		{"OTEL_EXPORTER_OTLP_INSECURE", "maybe"},
		{"OTEL_EXPORTER_OTLP_COMPRESSION", "zstd"},
		{"OTEL_EXPORTER_OTLP_METRICS_HEADERS", "novalue"},
		{"OTEL_EXPORTER_OTLP_HEADERS", "k=%zz"},
		{"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "lowmemory"},
		{"OTEL_METRIC_EXPORT_INTERVAL", "0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "c:4317")
			t.Setenv(tt.name, tt.value)

			_, err := NewClient(WithOTLPFromEnv())

			require.ErrorIs(t, err, ErrInvalidConfig)
			require.ErrorContains(t, err, tt.name)
		})
	}
}

func TestInvalidTimeoutEnvFailsNewClient(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317")
	t.Setenv("OTEL_EXPORTER_OTLP_TIMEOUT", "abc")

	client, err := NewClient(WithOTLPFromEnv())

	require.Nil(t, client)
	require.ErrorContains(t, err, "OTEL_EXPORTER_OTLP_TIMEOUT")
}

func TestExplicitOptionsIgnoreInvalidEnv(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_TIMEOUT", "abc")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/json")

	cfg := newConfigFrom(t, WithOTLP(&OTLPConfig{Endpoint: "c:4317", ExportTimeout: time.Second, Protocol: OTLPProtocolGRPC}))

	require.Equal(t, time.Second, cfg.OTLP.ExportTimeout)
}
