package stats

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/grpc"
)

// slowCollector is an in-process OTLP/gRPC metrics service that takes `delay`
// to answer each export, like a collector behind a real network hop.
type slowCollector struct {
	collectormetricspb.UnimplementedMetricsServiceServer
	delay time.Duration
	mu    sync.Mutex
	names map[string]struct{}
}

func (c *slowCollector) Export(ctx context.Context, req *collectormetricspb.ExportMetricsServiceRequest) (*collectormetricspb.ExportMetricsServiceResponse, error) {
	select {
	case <-time.After(c.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, resource := range req.GetResourceMetrics() {
		for _, scope := range resource.GetScopeMetrics() {
			for _, metric := range scope.GetMetrics() {
				c.names[metric.GetName()] = struct{}{}
			}
		}
	}
	return &collectormetricspb.ExportMetricsServiceResponse{}, nil
}

func (c *slowCollector) received(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.names[name]
	return ok
}

func startSlowCollector(t *testing.T, delay time.Duration) (collector *slowCollector, endpoint string) {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	collector = &slowCollector{delay: delay, names: make(map[string]struct{})}
	server := grpc.NewServer()
	collectormetricspb.RegisterMetricsServiceServer(server, collector)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return collector, listener.Addr().String()
}

func TestClient_OTLPgRPC_delivers_metrics_when_collector_is_slower_than_UDPTimeout(t *testing.T) {
	// Given: a collector whose export round trip (300ms) exceeds the default
	// 100ms UDPTimeout, and a client that flushes in the background.
	collector, endpoint := startSlowCollector(t, 300*time.Millisecond)
	client, err := NewClient(
		WithServiceName("svc"),
		WithOTLP(&OTLPConfig{Endpoint: endpoint, Insecure: true}),
	)
	require.NoError(t, err)

	// When: observations are recorded and the background flush interval passes.
	require.NoError(t, client.Counter(context.Background(), "http.server.requests", 1))
	require.Eventually(t, func() bool { return collector.received("http.server.requests") },
		5*time.Second, 20*time.Millisecond)

	// Then: shutdown reports no failed in-flight export.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, client.Shutdown(ctx))
}

func TestClient_OTLPgRPC_shutdown_during_export_succeeds(t *testing.T) {
	// Given: a slow collector and a record whose background export is in flight
	// (FlushInterval elapsed) when Shutdown is called.
	collector, endpoint := startSlowCollector(t, 300*time.Millisecond)
	client, err := NewClient(
		WithServiceName("svc"),
		WithOTLP(&OTLPConfig{Endpoint: endpoint, Insecure: true}),
	)
	require.NoError(t, err)
	require.NoError(t, client.Counter(context.Background(), "http.server.requests", 1))
	time.Sleep(200 * time.Millisecond)

	// When: Shutdown runs during that export.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = client.Shutdown(ctx)

	// Then: no deadline error, and the metric reached the collector.
	require.NoError(t, err)
	require.True(t, collector.received("http.server.requests"))
}
