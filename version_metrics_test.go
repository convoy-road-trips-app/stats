package stats

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

// versionCapture collects every exported metric, deep-copying attributes.
type versionCapture struct {
	mu      sync.Mutex
	metrics []*Metric
}

func (v *versionCapture) exporter() *MockExporter {
	return &MockExporter{name: "capture", exportFunc: func(_ context.Context, ms []*Metric) error {
		v.mu.Lock()
		defer v.mu.Unlock()
		for _, m := range ms {
			c := *m
			c.Attributes = append([]attribute.KeyValue(nil), m.Attributes...)
			v.metrics = append(v.metrics, &c)
		}
		return nil
	}}
}

func (v *versionCapture) named(name string) []*Metric {
	v.mu.Lock()
	defer v.mu.Unlock()
	var out []*Metric
	for _, m := range v.metrics {
		if m.Name == name {
			out = append(out, m)
		}
	}
	return out
}

func newVersionClient(t *testing.T, capture *versionCapture, opts ...Option) *Client {
	t.Helper()
	all := append([]Option{WithServiceName("svc"), WithEnvironment("prod"), WithExporter(capture.exporter())}, opts...)
	client, err := NewClient(all...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func attrMap(m *Metric) map[string]string {
	out := map[string]string{}
	for _, kv := range m.Attributes {
		out[string(kv.Key)] = kv.Value.String()
	}
	return out
}

func TestVersionMetricsOnce(t *testing.T) {
	// Given: a root client with default version reporting
	capture := &versionCapture{}
	client := newVersionClient(t, capture)
	ctx := context.Background()

	// When: it records several metrics
	for range 3 {
		require.NoError(t, client.Counter(ctx, "requests", 1))
	}
	require.NoError(t, client.Flush(ctx))

	// Then: each version gauge is exported exactly once, with value 1
	sv := capture.named("stats_version")
	require.Len(t, sv, 1)
	require.Equal(t, MetricTypeGauge, sv[0].Type)
	require.InDelta(t, 1, sv[0].Value, 0)
	require.Equal(t, map[string]string{"service": "svc", "environment": "prod", "stats_version": statsVersion()}, attrMap(sv[0]))

	if strings.HasPrefix(runtime.Version(), "devel") {
		require.Empty(t, capture.named("go_version"))
	} else {
		gv := capture.named("go_version")
		require.Len(t, gv, 1)
		require.Equal(t, MetricTypeGauge, gv[0].Type)
		require.InDelta(t, 1, gv[0].Value, 0)
		require.Equal(t, map[string]string{"service": "svc", "environment": "prod", "go_version": runtime.Version()}, attrMap(gv[0]))
	}
	require.Len(t, capture.named("requests"), 3)
}

func TestVersionMetricsNotRecordedByFailedRecord(t *testing.T) {
	// Given: a client and an invalid record
	capture := &versionCapture{}
	client := newVersionClient(t, capture)

	// When: the only record is rejected
	require.Error(t, client.Counter(context.Background(), "", 1))
	require.NoError(t, client.Flush(context.Background()))

	// Then: nothing is reported
	require.Empty(t, capture.named("stats_version"))
}

func TestVersionMetricsCarryNoViewOrContextTags(t *testing.T) {
	// Given: a root client with a view and a context carrying tags
	capture := &versionCapture{}
	client := newVersionClient(t, capture)
	ctx := ContextWithTags(context.Background(), attribute.String("ctx_tag", "x"))
	root := client

	// When: the first record happens on the root with context tags, then through a tagged view
	view := client.WithPrefix("api", WithAttribute("view_tag", "y"))
	require.NoError(t, root.Counter(ctx, "first", 1))
	require.NoError(t, view.Counter(ctx, "second", 1))
	require.NoError(t, client.Flush(context.Background()))

	// Then: version metrics have only the service attributes plus their own
	for _, name := range []string{"stats_version", "go_version"} {
		for _, m := range capture.named(name) {
			attrs := attrMap(m)
			require.NotContains(t, attrs, "ctx_tag")
			require.NotContains(t, attrs, "view_tag")
			require.Len(t, attrs, 3)
		}
	}
	require.Len(t, capture.named("stats_version"), 1)
}

func TestVersionMetricsOnlyFromRoot(t *testing.T) {
	// Given: a view that records first
	capture := &versionCapture{}
	client := newVersionClient(t, capture)

	// When: only the view records
	require.NoError(t, client.WithPrefix("api").Counter(context.Background(), "x", 1))
	require.NoError(t, client.Flush(context.Background()))

	// Then: no version metrics until the root records
	require.Empty(t, capture.named("stats_version"))
	require.NoError(t, client.Counter(context.Background(), "y", 1))
	require.NoError(t, client.Flush(context.Background()))
	require.Len(t, capture.named("stats_version"), 1)
}

func TestVersionReportingDisabledByOption(t *testing.T) {
	// Given: reporting disabled by option
	capture := &versionCapture{}
	client := newVersionClient(t, capture, WithVersionReporting(false))

	// When
	require.NoError(t, client.Counter(context.Background(), "requests", 1))
	require.NoError(t, client.Flush(context.Background()))

	// Then: zero version metrics
	require.Empty(t, capture.named("stats_version"))
	require.Empty(t, capture.named("go_version"))
	require.Len(t, capture.named("requests"), 1)
}

func TestVersionReportingDisabledByEnv(t *testing.T) {
	for _, value := range []string{"true", "TRUE", "yes", "1", "on"} {
		t.Run(value, func(t *testing.T) {
			// Given: the environment disables reporting
			t.Setenv(envDisableVersionReporting, value)
			capture := &versionCapture{}
			client := newVersionClient(t, capture)

			// When
			require.NoError(t, client.Counter(context.Background(), "requests", 1))
			require.NoError(t, client.Flush(context.Background()))

			// Then: both gauges are disabled
			require.Empty(t, capture.named("stats_version"))
			require.Empty(t, capture.named("go_version"))
		})
	}
}

func TestVersionReportingEnvIgnoresOtherValues(t *testing.T) {
	// Given: an environment value outside true|TRUE|yes|1|on
	t.Setenv(envDisableVersionReporting, "false")
	capture := &versionCapture{}
	client := newVersionClient(t, capture)

	// When
	require.NoError(t, client.Counter(context.Background(), "requests", 1))
	require.NoError(t, client.Flush(context.Background()))

	// Then: reporting stays on
	require.Len(t, capture.named("stats_version"), 1)
}

func TestVersionReportingOptionBeatsEnv(t *testing.T) {
	// Given: the environment disables reporting but the option enables it
	t.Setenv(envDisableVersionReporting, "true")
	capture := &versionCapture{}
	client := newVersionClient(t, capture, WithVersionReporting(true))

	// When
	require.NoError(t, client.Counter(context.Background(), "requests", 1))
	require.NoError(t, client.Flush(context.Background()))

	// Then: the option wins
	require.Len(t, capture.named("stats_version"), 1)
}

func TestVersionMetricsNotRecordedByDisabledClient(t *testing.T) {
	// Given: a disabled client (OTEL_SDK_DISABLED) with an exporter and reporting forced on
	t.Setenv("OTEL_SDK_DISABLED", "true")
	capture := &versionCapture{}
	client := newVersionClient(t, capture, WithVersionReporting(true))

	// When: it records
	require.NoError(t, client.Counter(context.Background(), "requests", 1))
	require.NoError(t, client.Flush(context.Background()))

	// Then: nothing is exported, version metrics included
	require.Empty(t, capture.named("stats_version"))
	require.Empty(t, capture.named("go_version"))
	require.Empty(t, capture.named("requests"))
}
