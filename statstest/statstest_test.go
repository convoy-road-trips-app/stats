package statstest_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats"
	"github.com/convoy-road-trips-app/stats/models"
	"github.com/convoy-road-trips-app/stats/statstest"
)

func TestExporterCapturesCopies(t *testing.T) {
	ts := time.Unix(1700000000, 0)
	src := &models.Metric{
		Name:       "requests",
		Type:       models.MetricTypeCounter,
		Value:      3,
		Attributes: []attribute.KeyValue{attribute.String("route", "/a")},
		Timestamp:  ts,
	}
	exp := statstest.NewExporter()
	require.NoError(t, exp.Export(context.Background(), []*models.Metric{src}))

	// The pipeline reuses released metrics: simulate by overwriting the source.
	src.Name = "reused"
	src.Type = models.MetricTypeGauge
	src.Value = 99
	src.Attributes[0] = attribute.String("route", "/b")
	src.Timestamp = time.Time{}

	want := models.Metric{
		Name:       "requests",
		Type:       models.MetricTypeCounter,
		Value:      3,
		Attributes: []attribute.KeyValue{attribute.String("route", "/a")},
		Timestamp:  ts,
	}
	require.Equal(t, []models.Metric{want}, exp.Metrics())

	// Mutating a returned copy must not change the capture either.
	got := exp.Metrics()
	got[0].Attributes[0] = attribute.String("route", "/c")
	got[0].Name = "mutated"
	require.Equal(t, []models.Metric{want}, exp.Metrics())
}

func TestExporterSkipsNilMetrics(t *testing.T) {
	exp := statstest.NewExporter()
	require.NoError(t, exp.Export(context.Background(), []*models.Metric{nil, {Name: "a"}}))
	require.Len(t, exp.Metrics(), 1)
}

func TestClear(t *testing.T) {
	exp := statstest.NewExporter()
	require.NoError(t, exp.Export(context.Background(), []*models.Metric{{Name: "a"}}))
	require.Len(t, exp.Metrics(), 1)
	require.Equal(t, 1, exp.FlushCalls())

	exp.Clear()

	require.Empty(t, exp.Metrics())
	require.Zero(t, exp.FlushCalls())
}

func TestNewClientHelperFlush(t *testing.T) {
	client, exp := statstest.NewClient(t)
	ctx := context.Background()

	require.NoError(t, client.Counter(ctx, "hits", 2, stats.WithAttribute("route", "/a")))
	statstest.Flush(t, client)

	var found []models.Metric
	for _, m := range exp.Metrics() {
		if m.Name == "hits" {
			found = append(found, m)
		}
	}
	require.Len(t, found, 1)
	require.Equal(t, models.MetricTypeCounter, found[0].Type)
	require.InDelta(t, 2, found[0].Value, 0)
	require.Contains(t, found[0].Attributes, attribute.String("route", "/a"))
	require.False(t, found[0].Timestamp.IsZero())
	require.Positive(t, exp.FlushCalls())

	// Flush again with nothing buffered: no new Export call, no new metrics.
	calls := exp.FlushCalls()
	statstest.Flush(t, client)
	require.Equal(t, calls, exp.FlushCalls())
}

func TestNewClientClosesClientInCleanup(t *testing.T) {
	var client *stats.Client
	t.Run("inner", func(t *testing.T) {
		client, _ = statstest.NewClient(t)
	})
	require.ErrorIs(t, client.Flush(context.Background()), stats.ErrClientClosed)
}
