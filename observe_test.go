package stats_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/convoy-road-trips-app/stats"
	"github.com/convoy-road-trips-app/stats/models"
	"github.com/convoy-road-trips-app/stats/statstest"
)

var (
	_ stats.DurationObserver = (*stats.Client)(nil)
	_ stats.DurationObserver = (*stats.NoOpClient)(nil)
)

// captured returns the single metric named name, failing the test otherwise.
func captured(t *testing.T, exp *statstest.Exporter, name string) models.Metric {
	t.Helper()
	var found []models.Metric
	all := exp.Metrics()
	for i := range all {
		if all[i].Name == name {
			found = append(found, all[i])
		}
	}
	require.Len(t, found, 1)
	return found[0]
}

func TestObserveRecordsSeconds(t *testing.T) {
	client, exp := statstest.NewClient(t)

	require.NoError(t, client.Observe(context.Background(), "op.duration", 1500*time.Millisecond))
	statstest.Flush(t, client)

	m := captured(t, exp, "op.duration")
	require.InDelta(t, 1.5, m.Value, 1e-9)
	require.Equal(t, models.MetricTypeHistogram, m.Type)
}

func TestTimingUnchanged(t *testing.T) {
	client, exp := statstest.NewClient(t)

	require.NoError(t, client.Timing(context.Background(), "op.timing", 1500*time.Millisecond))
	statstest.Flush(t, client)

	m := captured(t, exp, "op.timing")
	require.InDelta(t, 1500.0, m.Value, 1e-9)
	require.Equal(t, models.MetricTypeHistogram, m.Type)
}

func TestObserveAppliesOptions(t *testing.T) {
	client, exp := statstest.NewClient(t)

	require.NoError(t, client.Observe(context.Background(), "op.tagged", time.Second,
		stats.WithAttribute("route", "/x")))
	statstest.Flush(t, client)

	m := captured(t, exp, "op.tagged")
	var route string
	for _, kv := range m.Attributes {
		if string(kv.Key) == "route" {
			route = kv.Value.AsString()
		}
	}
	require.Equal(t, "/x", route)
}

func TestNoOpObserve(t *testing.T) {
	var o stats.DurationObserver = stats.NewNoOpClient()
	require.NoError(t, o.Observe(context.Background(), "op.duration", time.Second))
}

func TestObserveAfterCloseReturnsErrClientClosed(t *testing.T) {
	client, err := stats.NewClient()
	require.NoError(t, err)
	require.NoError(t, client.Close())

	err = client.Observe(context.Background(), "op.duration", time.Second)
	require.ErrorIs(t, err, stats.ErrClientClosed)
}
