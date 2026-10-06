package stats

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/convoy-road-trips-app/stats/exporters/prometheus"
	"github.com/convoy-road-trips-app/stats/models"
	"github.com/stretchr/testify/require"
)

func TestWithHistogramBucketsForCopies(t *testing.T) {
	// Given: caller-owned bounds
	bounds := []float64{1, 2, 3}
	config := DefaultConfig()
	WithHistogramBucketsFor("checkout_latency_seconds", bounds...)(config)

	// When: the caller reuses its slice
	bounds[0] = 99

	// Then
	require.Equal(t, []float64{1, 2, 3}, config.HistogramBucketsByName["checkout_latency_seconds"])
}

func TestWithHistogramBucketsFor_reused_option_gives_each_config_its_own_slice(t *testing.T) {
	// Given
	option := WithHistogramBucketsFor("m", 1, 2, 3)
	first, second := DefaultConfig(), DefaultConfig()
	option(first)
	first.HistogramBucketsByName["m"][0] = 0.5

	// When
	option(second)

	// Then
	require.Equal(t, []float64{1, 2, 3}, second.HistogramBucketsByName["m"])
}

func TestNewClient_rejects_invalid_per_name_buckets(t *testing.T) {
	cases := map[string][]float64{
		"non-increasing": {1, 1, 2},
		"decreasing":     {3, 2, 1},
		"empty":          {},
	}
	for name, bounds := range cases {
		t.Run(name, func(t *testing.T) {
			// When
			client, err := NewClient(WithHistogramBucketsFor("m", bounds...))

			// Then
			require.Error(t, err)
			require.Nil(t, client)
		})
	}
}

func TestNewClient_rejects_non_finite_per_name_buckets(t *testing.T) {
	_, err := NewClient(WithHistogramBucketsFor("m", 1, math.Inf(1)))
	require.Error(t, err)
}

func TestNewClient_accepts_valid_per_name_buckets(t *testing.T) {
	client, err := NewClient(WithHistogramBucketsFor("m", 0.1, 1, 10))
	require.NoError(t, err)
	require.NoError(t, client.Close())
}

func TestBucketsForPrecedence(t *testing.T) {
	byName := map[string][]float64{"named": {1, 2}}
	global := []float64{5, 6}

	require.Equal(t, []float64{1, 2}, models.BucketsFor(byName, global, "named"))
	require.Equal(t, []float64{5, 6}, models.BucketsFor(byName, global, "other"))
	require.Equal(t, []float64{5, 6}, models.BucketsFor(nil, global, "other"))
	require.Equal(t, models.DefaultHistogramBuckets(), models.BucketsFor(byName, nil, "other"))
	require.Equal(t, models.DefaultHistogramBuckets(), models.BucketsFor(nil, nil, "other"))
}

func TestBucketsForUnprefixed(t *testing.T) {
	byName := map[string][]float64{
		"app.request.duration":                          {1},
		models.UnprefixedBucketsKey("request.duration"): {2},
		models.UnprefixedBucketsKey("duration"):         {3},
		"plain":                                         {4},
	}
	global := []float64{9}

	require.Equal(t, []float64{1}, models.BucketsFor(byName, global, "app.request.duration"), "exact beats suffix")
	require.Equal(t, []float64{2}, models.BucketsFor(byName, global, "request.duration"), "full name is the longest suffix")
	require.Equal(t, []float64{2}, models.BucketsFor(byName, global, "myapp.request.duration"), "longest suffix wins")
	require.Equal(t, []float64{3}, models.BucketsFor(byName, global, "myapp.db.duration"))
	require.Equal(t, global, models.BucketsFor(byName, global, "myapp.latency"))
	require.Equal(t, global, models.BucketsFor(byName, global, "myplain"), "no boundary match")
	require.Equal(t, global, models.BucketsFor(byName, global, "myapp.plain"), "plain entries are exact only")
	require.Equal(t, []float64{3}, models.BucketsFor(byName, global, "xrequest.duration"), "request.duration does not match mid-segment; duration does")
	require.Equal(t, global, models.BucketsFor(byName, global, "xduration"), "suffix must start at a segment")
}

func TestWithUnprefixedHistogramBucketsFor(t *testing.T) {
	cfg := DefaultConfig()
	WithUnprefixedHistogramBucketsFor("request.duration", 1, 2)(cfg)
	require.Equal(t, []float64{1, 2}, cfg.HistogramBucketsByName["*.request.duration"])

	_, err := NewClient(WithUnprefixedHistogramBucketsFor("m", 2, 1))
	require.Error(t, err)
}

func TestUnprefixedBucketsReachPrometheusHandler(t *testing.T) {
	h := &prometheus.Handler{}
	client, err := NewClient(WithFlushInterval(time.Hour), WithPrometheusHandler(h),
		WithUnprefixedHistogramBucketsFor("request.duration", 1, 2))
	require.NoError(t, err)
	defer client.Close()

	ctx := context.Background()
	require.NoError(t, client.Histogram(ctx, "myapp.request.duration", 0.5))
	require.NoError(t, client.Flush(ctx))
	body := scrape(t, h)
	require.Contains(t, body, `myapp_request_duration_bucket{le="1"} 1`+"\n")
	require.Contains(t, body, `myapp_request_duration_bucket{le="2"} 1`+"\n")
	require.NotContains(t, body, `le="0.005"`)
}
