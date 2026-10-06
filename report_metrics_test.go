package stats

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

type makeMetricsFixture struct {
	Service string `tag:"service"`
	Calls   struct {
		Count int           `metric:"count" type:"counter"`
		Time  time.Duration `metric:"time"`
		Up    bool          `metric:"up" type:"gauge"`
	} `metric:"func.calls"`
}

func TestMakeMetrics(t *testing.T) {
	var v makeMetricsFixture
	v.Service = "api"
	v.Calls.Count = 3
	v.Calls.Time = 1500 * time.Millisecond
	v.Calls.Up = true

	ts := time.Unix(100, 0)
	got, err := MakeMetrics(&v, WithTimestamp(ts), WithAttribute("extra", "x"))
	require.NoError(t, err)
	require.Len(t, got, 3)

	byName := map[string]*Metric{}
	for _, m := range got {
		byName[m.Name] = m
	}
	count := byName["func.calls.count"]
	require.Equal(t, MetricTypeCounter, count.Type)
	require.InDelta(t, 3.0, count.Value, 0)
	require.Equal(t, []attribute.KeyValue{attribute.String("service", "api"), attribute.String("extra", "x")}, count.Attributes)
	require.Equal(t, ts, count.Timestamp)
	require.Equal(t, MetricTypeHistogram, byName["func.calls.time"].Type)
	require.InDelta(t, 1.5, byName["func.calls.time"].Value, 0)
	require.Equal(t, MetricTypeGauge, byName["func.calls.up"].Type)
	require.InDelta(t, 1.0, byName["func.calls.up"].Value, 0)
}

func TestMakeMetricsMatchesReport(t *testing.T) {
	items := []makeMetricsFixture{{Service: "a"}, {Service: "b"}}
	items[1].Calls.Count = 7

	made, err := MakeMetrics(items)
	require.NoError(t, err)

	capture := &versionCapture{}
	client := newVersionClient(t, capture, WithVersionReporting(false))
	require.NoError(t, Report(context.Background(), client, items))
	require.NoError(t, client.Flush(context.Background()))

	key := func(m *Metric) string { return m.Name + "/" + attrMap(m)["service"] }
	want := map[string]*Metric{}
	for _, m := range made {
		want[key(m)] = m
	}
	require.Len(t, capture.metrics, len(made))
	for _, got := range capture.metrics {
		m, ok := want[key(got)]
		require.True(t, ok, key(got))
		require.Equal(t, m.Type, got.Type)
		require.InDelta(t, m.Value, got.Value, 0)
	}
}

func TestMakeMetricsAreReusable(t *testing.T) {
	made, err := MakeMetrics(&makeMetricsFixture{Service: "a"})
	require.NoError(t, err)
	copyOf := made[0].Clone()
	made[0].Attributes[0] = attribute.String("service", "changed")
	require.Equal(t, "a", copyOf.Attributes[0].Value.AsString())
	require.Equal(t, 1, int(made[0].Priority), "default priority")
}

func TestMakeMetricsNilAndErrors(t *testing.T) {
	got, err := MakeMetrics(nil)
	require.NoError(t, err)
	require.Empty(t, got)

	_, err = MakeMetrics(struct {
		C chan int `metric:"c"`
	}{})
	require.ErrorIs(t, err, ErrUnsupportedReportField)

	_, err = MakeMetrics(42)
	require.ErrorIs(t, err, ErrUnsupportedReportField)
}
