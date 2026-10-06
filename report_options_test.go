package stats_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/convoy-road-trips-app/stats"
	"github.com/convoy-road-trips-app/stats/statstest"
)

func TestReportRecursiveType(t *testing.T) {
	client, _ := statstest.NewClient(t)
	require.ErrorIs(t, stats.Report(context.Background(), client, &reportNode{V: 1}), stats.ErrUnsupportedReportField)
}

func TestReportNilAndEmpty(t *testing.T) {
	client, exp := statstest.NewClient(t)
	ctx := context.Background()

	var np *funcMetrics
	var nilIface any
	require.NoError(t, stats.Report(ctx, client, nil))
	require.NoError(t, stats.Report(ctx, client, np))
	require.NoError(t, stats.Report(ctx, client, nilIface))
	require.NoError(t, stats.Report(ctx, client, []funcMetrics(nil)))
	require.NoError(t, stats.Report(ctx, client, struct{ X int }{1}))
	require.ErrorIs(t, stats.Report(ctx, nil, funcMetrics{}), stats.ErrInvalidConfig)

	statstest.Flush(t, client)
	require.Empty(t, namedMetrics(exp, "func.calls.count"))
}

func TestReportPrefixAndContextTags(t *testing.T) {
	root, exp := statstest.NewClient(t)
	view := root.WithPrefix("svc", stats.WithAttribute("env", "test"))

	var m funcMetrics
	m.calls.count = 1
	require.NoError(t, stats.Report(context.Background(), view, m))
	statstest.Flush(t, root)

	got := captured(t, exp, "svc.func.calls.count")
	env, _ := attrValue(&got, "env")
	require.Equal(t, "test", env)
}

func TestReportAt(t *testing.T) {
	client, exp := statstest.NewClient(t)
	ts := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)

	var m funcMetrics
	m.calls.count = 1
	require.NoError(t, stats.ReportAt(context.Background(), client, ts, m, stats.WithTimestamp(time.Unix(1, 0))))
	statstest.Flush(t, client)

	require.True(t, captured(t, exp, "func.calls.count").Timestamp.Equal(ts))
}

func TestReportJoinsRecordErrors(t *testing.T) {
	type v struct {
		A float64 `metric:"a"`
		B float64 `metric:"b"`
	}
	client, _ := statstest.NewClient(t)
	nan := v{A: nanValue(), B: nanValue()}
	err := stats.Report(context.Background(), client, nan)
	require.Error(t, err)
	require.NotErrorIs(t, err, stats.ErrUnsupportedReportField)
}

func BenchmarkReport(b *testing.B) {
	type bench struct {
		Service string  `tag:"service"`
		Reqs    int     `metric:"reqs" type:"counter"`
		Secs    float64 `metric:"secs"`
		Calls   struct {
			Count int           `metric:"count" type:"counter"`
			Time  time.Duration `metric:"time"`
		} `metric:"calls"`
	}
	v := bench{Service: "api", Reqs: 1, Secs: 0.1}
	v.Calls.Count = 1
	v.Calls.Time = time.Millisecond

	client := stats.NewNoOpClient()
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if err := stats.Report(ctx, client, &v); err != nil {
			b.Fatal(err)
		}
	}
}
