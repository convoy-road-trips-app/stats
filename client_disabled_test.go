package stats

import (
	"context"
	"math"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

// acceptCounter listens on a loopback TCP port and counts every accepted
// connection, so a test can prove nothing dialed it.
func acceptCounter(t *testing.T) (addr string, accepts *atomic.Int64) {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	accepts = new(atomic.Int64)
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			accepts.Add(1)
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() { _ = l.Close() })
	return l.Addr().String(), accepts
}

func newDisabledClient(t *testing.T, opts ...Option) *Client {
	t.Helper()
	t.Setenv("OTEL_SDK_DISABLED", "true")
	client, err := NewClient(opts...)
	require.NoError(t, err)
	require.NotNil(t, client)
	return client
}

func TestDisabledAcceptsTrueCaseInsensitively(t *testing.T) {
	for _, value := range []string{"true", "TRUE", "True", " true "} {
		t.Setenv("OTEL_SDK_DISABLED", value)
		client, err := NewClient()
		require.NoError(t, err, value)
		require.True(t, client.Disabled(), value)
	}
	for _, value := range []string{"", "false", "1", "yes"} {
		t.Setenv("OTEL_SDK_DISABLED", value)
		client, err := NewClient()
		require.NoError(t, err, value)
		require.False(t, client.Disabled(), value)
		require.NoError(t, client.Close())
	}
}

func TestDisabledStartsNothing(t *testing.T) {
	client := newDisabledClient(t, WithRuntimeMetrics(), WithWorkers(4))
	require.Nil(t, client.core.pipeline)
	require.Nil(t, client.core.collector)
}

func TestDisabledNeverDials(t *testing.T) {
	addr, accepts := acceptCounter(t)
	client := newDisabledClient(t,
		WithOTLP(&OTLPConfig{Endpoint: addr, Insecure: true, Protocol: OTLPProtocolGRPC}),
		WithDatadog(&DatadogConfig{Endpoint: "tcp-not-udp"}), // invalid on purpose: never validated
		WithRuntimeMetrics(),
	)
	ctx := context.Background()
	require.NoError(t, client.Counter(ctx, "dial.check", 1))
	require.NoError(t, client.Flush(ctx))
	require.NoError(t, client.Shutdown(ctx))
	time.Sleep(100 * time.Millisecond)
	require.Zero(t, accepts.Load())
}

func TestDisabledRecordingMethodsReturnNil(t *testing.T) {
	client := newDisabledClient(t)
	ctx := context.Background()
	view := client.WithPrefix("api").WithTags(WithAttribute("k", "v"))
	require.True(t, view.Disabled())

	for _, c := range []*Client{client, view} {
		require.NoError(t, c.Counter(ctx, "c", 1))
		require.NoError(t, c.Gauge(ctx, "g", 1))
		require.NoError(t, c.Histogram(ctx, "h", 1))
		require.NoError(t, c.Increment(ctx, "i"))
		require.NoError(t, c.IncrementBy(ctx, "i", 2))
		require.NoError(t, c.Timing(ctx, "t", time.Second))
		require.NoError(t, c.Observe(ctx, "o", time.Second))
		require.NoError(t, c.RecordMetric(ctx, NewCounter("m", 1).Build()))
		require.NoError(t, c.Flush(ctx))
		require.NoError(t, c.Shutdown(ctx))
		require.NoError(t, c.Close())
	}
}

func TestDisabledDoesNotValidateInput(t *testing.T) {
	client := newDisabledClient(t)
	ctx := context.Background()
	long := strings.Repeat("x", 300)
	require.NoError(t, client.Counter(ctx, "", 1))
	require.NoError(t, client.Counter(ctx, long, 1))
	require.NoError(t, client.Gauge(ctx, "g", math.NaN()))
	require.NoError(t, client.Histogram(ctx, "h", math.Inf(1)))
	require.NoError(t, client.Counter(ctx, "c", 1, WithAttribute("bad key!", "v"), withKeyValues([]attribute.KeyValue{attribute.String("", "x")})))
	require.NoError(t, client.RecordMetric(ctx, &Metric{}))
	require.NoError(t, client.WithPrefix("p").Counter(ctx, "", 1))
}

func TestDisabledRecordAfterShutdownStillNil(t *testing.T) {
	client := newDisabledClient(t)
	ctx := context.Background()
	require.NoError(t, client.Shutdown(ctx))
	require.NoError(t, client.Counter(ctx, "c", 1))
	require.NoError(t, client.Flush(ctx))
}

func TestDisabledStats(t *testing.T) {
	client := newDisabledClient(t, WithServiceName("svc"))
	view := client.WithPrefix("p")
	ctx := context.Background()

	check := func() {
		for _, c := range []*Client{client, view} {
			require.NotPanics(t, func() {
				got := c.Stats()
				require.Equal(t, ClientStats{Pipeline: PipelineStats{ExporterErrors: map[string]uint64{}}}, got)
				require.NotNil(t, got.Pipeline.ExporterErrors)
				require.Empty(t, got.Pipeline.ExporterErrors)
			})
		}
	}
	check()
	require.NoError(t, client.Shutdown(ctx))
	check()
}
