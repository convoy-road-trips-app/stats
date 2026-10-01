//go:build integration

package lgtm

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/convoy-road-trips-app/stats"
	statsOtel "github.com/convoy-road-trips-app/stats/otel"
)

// observations covers the first bucket, a repeated interior bucket, the last
// finite bucket and the +Inf overflow for the D9 default bounds.
var observations = []float64{0.003, 0.02, 0.02, 0.3, 7, 42}

// bucketCase is one histogram path to Prometheus with the cumulative _bucket
// counts Prometheus must report per le bound (math.Inf(1) is le="+Inf").
type bucketCase struct {
	name   string
	record func(t *testing.T, metricName string, attrs attribute.Set)
	want   map[float64]float64
}

func TestLGTM_HistogramBuckets(t *testing.T) {
	runID := strconv.FormatInt(time.Now().UnixNano(), 36)
	d9 := map[float64]float64{
		0.005: 1, 0.01: 1, 0.025: 3, 0.05: 3, 0.1: 3, 0.25: 3,
		0.5: 4, 1: 4, 2.5: 4, 5: 4, 10: 5, math.Inf(1): 6,
	}

	cases := []bucketCase{
		{name: "legacy_default", record: recordLegacy(nil), want: d9},
		{
			name:   "legacy_custom",
			record: recordLegacy([]stats.Option{stats.WithHistogramBuckets([]float64{0.01, 1, 10})}),
			want:   map[float64]float64{0.01: 1, 1: 4, 10: 5, math.Inf(1): 6},
		},
		{name: "otel_default", record: recordOTel, want: d9},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a run-unique series so stale data from earlier runs never matches
			metricName := "lgtm_bucket_" + tc.name
			attrs := attribute.NewSet(attribute.String("run_id", runID))

			// When: the observations are exported over OTLP and the client shuts down
			tc.record(t, metricName, attrs)

			// Then: Prometheus serves one cumulative _bucket series per le bound
			query := fmt.Sprintf(`%s_bucket{run_id=%q}`, metricName, runID)
			got := waitForBuckets(t, query, len(tc.want))
			assert.Equal(t, tc.want, got)

			count := waitForMetric(t, fmt.Sprintf(`%s_count{run_id=%q}`, metricName, runID))
			assert.InDelta(t, float64(len(observations)), sampleValue(t, count.Data.Result[0]), 0)
		})
	}
}

func recordLegacy(extra []stats.Option) func(*testing.T, string, attribute.Set) {
	return func(t *testing.T, metricName string, attrs attribute.Set) {
		t.Helper()
		client := newOTLPClient(t, "lgtm-buckets", extra...)
		runID, _ := attrs.Value("run_id")
		for _, v := range observations {
			require.NoError(t, client.Histogram(context.Background(), metricName, v,
				stats.WithAttribute("run_id", runID.AsString())))
		}
		require.NoError(t, client.Close())
	}
}

func recordOTel(t *testing.T, metricName string, attrs attribute.Set) {
	t.Helper()
	provider, err := statsOtel.NewMeterProvider(statsOtel.WithStatsOptions(otlpOptions("lgtm-buckets-otel")...))
	require.NoError(t, err)
	hist, err := provider.Meter("lgtm-buckets").Float64Histogram(metricName)
	require.NoError(t, err)
	for _, v := range observations {
		hist.Record(context.Background(), v, metric.WithAttributeSet(attrs))
	}
	require.NoError(t, provider.Shutdown(context.Background()))
}

// waitForBuckets polls until query returns want series and parses them into
// le bound -> cumulative count.
func waitForBuckets(t *testing.T, query string, want int) map[float64]float64 {
	t.Helper()
	deadline := time.Now().Add(queryTimeout)
	for {
		resp := queryProm(t, query)
		if resp.Status == "success" && len(resp.Data.Result) >= want {
			buckets := make(map[float64]float64, len(resp.Data.Result))
			for _, r := range resp.Data.Result {
				le, err := strconv.ParseFloat(r.Metric["le"], 64)
				require.NoError(t, err, "le label %q", r.Metric["le"])
				buckets[le] = sampleValue(t, r)
			}
			return buckets
		}
		if time.Now().After(deadline) {
			t.Fatalf("want %d _bucket series within %v, got %d: query=%s", want, queryTimeout, len(resp.Data.Result), query)
		}
		time.Sleep(pollInterval)
	}
}

func sampleValue(t *testing.T, r promResult) float64 {
	t.Helper()
	require.Len(t, r.Value, 2)
	var raw string
	require.NoError(t, json.Unmarshal(r.Value[1], &raw))
	v, err := strconv.ParseFloat(raw, 64)
	require.NoError(t, err)
	return v
}
