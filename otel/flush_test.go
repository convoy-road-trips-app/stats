package otel

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/convoy-road-trips-app/stats"
	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

func TestMeterProvider_ForceFlush_delivers_buffered_observations_before_returning(t *testing.T) {
	// Given: a provider whose only export trigger is ForceFlush
	received := make(chan *collectormetricspb.ExportMetricsServiceRequest, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var request collectormetricspb.ExportMetricsServiceRequest
		if err := proto.Unmarshal(body, &request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		received <- &request
	}))
	defer server.Close()
	provider, err := NewMeterProvider(WithStatsOptions(
		stats.WithServiceName("force-flush"),
		stats.WithFlushInterval(time.Hour),
		stats.WithOTLP(&stats.OTLPConfig{
			Endpoint: strings.TrimPrefix(server.URL, "http://"),
			Insecure: true,
			Protocol: stats.OTLPProtocolHTTP,
		}),
	))
	if err != nil {
		t.Fatalf("create MeterProvider: %v", err)
	}
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	counter, err := provider.Meter("test").Int64Counter("requests_total")
	if err != nil {
		t.Fatalf("create counter: %v", err)
	}
	counter.Add(context.Background(), 3)

	// When
	err = provider.ForceFlush(context.Background())

	// Then: the export has already reached the receiver
	if err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
	select {
	case request := <-received:
		value := request.ResourceMetrics[0].ScopeMetrics[0].Metrics[0].GetSum().DataPoints[0].GetAsDouble()
		if value != 3 {
			t.Fatalf("requests_total = %v, want 3", value)
		}
	default:
		t.Fatal("ForceFlush returned before the OTLP receiver got the buffered observation")
	}
}

func TestMeterProvider_ForceFlush_after_Shutdown_returns_error(t *testing.T) {
	// Given
	provider, err := NewMeterProvider(WithStatsOptions(stats.WithServiceName("force-flush-closed")))
	if err != nil {
		t.Fatalf("create MeterProvider: %v", err)
	}
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// When
	err = provider.ForceFlush(context.Background())

	// Then
	if err == nil {
		t.Fatal("ForceFlush after Shutdown returned nil, want an error")
	}
}
