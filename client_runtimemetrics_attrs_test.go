package stats

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

func newRuntimeAttrsClient(t *testing.T, exp Exporter) *Client {
	t.Helper()
	client, err := NewClient(WithServiceName("rt-attrs"), WithExporter(exp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestRuntimeRecord_attributes_reach_the_exported_metric(t *testing.T) {
	// Given: a client whose exporter captures what it receives
	var mu sync.Mutex
	got := map[string][]attribute.KeyValue{}
	exp := &MockExporter{name: "capture", exportFunc: func(_ context.Context, ms []*Metric) error {
		mu.Lock()
		defer mu.Unlock()
		for _, m := range ms {
			got[m.Name] = append([]attribute.KeyValue(nil), m.Attributes...)
		}
		return nil
	}}
	client := newRuntimeAttrsClient(t, exp)

	// When: the runtime adapter records with and without attributes
	client.runtimeRecord("rt.cpu", MetricTypeCounter, 2, attribute.String("type", "user"), attribute.Int("n", 1))
	client.runtimeRecord("rt.plain", MetricTypeGauge, 1)
	require.NoError(t, client.Flush(context.Background()))

	// Then: the attributes are on the exported metric
	mu.Lock()
	defer mu.Unlock()
	require.ElementsMatch(t, []attribute.KeyValue{attribute.String("type", "user"), attribute.Int("n", 1)}, got["rt.cpu"])
	require.Empty(t, got["rt.plain"])
	require.Contains(t, got, "rt.plain")
}

func TestRuntimeConfig_OnError_increments_ExporterErrors(t *testing.T) {
	// Given: a client and a runtime metrics config
	client := newRuntimeAttrsClient(t, &MockExporter{name: "capture"})
	rc := &RuntimeMetricsConfig{ProcessMetrics: true, DelayMetrics: true}

	// When: the collector reports an error from source "x"
	cfg := client.runtimeConfig(rc)
	require.True(t, cfg.ProcessMetrics)
	require.True(t, cfg.DelayMetrics)
	cfg.OnError("x", context.DeadlineExceeded)
	cfg.OnError("x", context.DeadlineExceeded)

	// Then: it is counted under runtimemetrics.x
	ps := client.Stats().Pipeline
	require.Equal(t, uint64(2), ps.ExporterErrors["runtimemetrics.x"])
	require.Equal(t, uint64(0), ps.ExporterErrors["capture"])
	require.Equal(t, uint64(2), ps.Errors)
}

func TestRecordExporterError_unknown_name_is_safe(t *testing.T) {
	// Given: a pipeline with a single exporter
	client := newRuntimeAttrsClient(t, &MockExporter{name: "capture"})
	p := client.core.pipeline

	// When: errors are recorded for an unknown name and a known one, concurrently
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.RecordExporterError("nope")
			p.RecordExporterError("capture")
		}()
	}
	wg.Wait()

	// Then: nothing panics and both are counted
	ps := p.Stats()
	require.Equal(t, uint64(8), ps.ExporterErrors["nope"])
	require.Equal(t, uint64(8), ps.ExporterErrors["capture"])
}
