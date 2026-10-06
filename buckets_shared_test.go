package stats

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/convoy-road-trips-app/stats/exporters/prometheus"
	"github.com/convoy-road-trips-app/stats/models"
	"github.com/stretchr/testify/require"
)

func TestBucketsSharedAcrossOTLPAndPrometheus(t *testing.T) {
	const metricName = "req.dur"
	perNameBounds := []float64{0.1, 1}
	globalBounds := []float64{0.25, 2.5}

	tests := []struct {
		name       string
		metricName string
		options    []Option
		want       []float64
	}{
		{
			name:       "per-name bounds",
			metricName: metricName,
			options:    []Option{WithHistogramBucketsFor(metricName, perNameBounds...)},
			want:       perNameBounds,
		},
		{
			name:       "global fallback",
			metricName: "fallback.dur",
			options: []Option{
				WithHistogramBucketsFor(metricName, perNameBounds...),
				WithHistogramBuckets(globalBounds),
			},
			want: globalBounds,
		},
		{
			name:       "default fallback",
			metricName: "default.dur",
			options: []Option{
				WithHistogramBucketsFor(metricName, perNameBounds...),
			},
			want: models.DefaultHistogramBuckets(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given: one client whose OTLP and Prometheus exporters share bucket configuration.
			endpoint, received := boundsReceiver(t)
			handler := &prometheus.Handler{}
			options := []Option{
				WithFlushInterval(time.Hour),
				WithVersionReporting(false),
				WithOTLP(&OTLPConfig{
					Endpoint: endpoint,
					Insecure: true,
					Protocol: OTLPProtocolHTTP,
				}),
				WithPrometheusHandler(handler),
			}
			options = append(options, tt.options...)
			client, err := NewClient(options...)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, client.Shutdown(context.Background())) })

			// When: the client records and flushes one histogram.
			ctx := context.Background()
			require.NoError(t, client.Histogram(ctx, tt.metricName, 0.5))
			require.NoError(t, client.Flush(ctx))

			// Then: both exporters expose exactly the selected finite bounds.
			require.Eventually(t, func() bool { return len(received()) > 0 }, 5*time.Second, 10*time.Millisecond)
			require.Equal(t, tt.want, received()[tt.metricName])
			require.Equal(t, append(bucketStrings(tt.want), "+Inf"), prometheusHistogramBounds(t, handler, tt.metricName))
		})
	}
}

func bucketStrings(bounds []float64) []string {
	values := make([]string, len(bounds))
	for i, bound := range bounds {
		values[i] = strconv.FormatFloat(bound, 'g', -1, 64)
	}
	return values
}

func prometheusHistogramBounds(t *testing.T, handler *prometheus.Handler, metricName string) []string {
	t.Helper()
	name := strings.NewReplacer(".", "_").Replace(metricName)
	body := scrape(t, handler)
	prefix := name + `_bucket{le="`
	var bounds []string
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		bound, _, ok := strings.Cut(strings.TrimPrefix(line, prefix), `"}`)
		require.True(t, ok, "malformed Prometheus histogram bucket line %q", line)
		bounds = append(bounds, bound)
	}
	return bounds
}
