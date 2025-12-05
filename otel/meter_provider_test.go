package otel

import (
	"context"
	"testing"

	"github.com/convoy-road-trips-app/stats"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

func TestMeterProvider_Basic(t *testing.T) {
	// Create a MeterProvider with stats options
	provider, err := NewMeterProvider(
		WithStatsOptions(
			stats.WithServiceName("test-service"),
			stats.WithEnvironment("test"),
			stats.WithBufferSize(1024),
		),
	)
	if err != nil {
		t.Fatalf("Failed to create MeterProvider: %v", err)
	}
	defer provider.Shutdown(context.Background())

	// Get a meter
	meter := provider.Meter("test-meter")
	if meter == nil {
		t.Fatal("Meter is nil")
	}

	// Create an Int64Counter
	counter, err := meter.Int64Counter("test.counter")
	if err != nil {
		t.Fatalf("Failed to create counter: %v", err)
	}

	// Record some metrics
	ctx := context.Background()
	counter.Add(ctx, 1, metric.WithAttributes(attribute.String("key", "value")))
	counter.Add(ctx, 5, metric.WithAttributes(attribute.String("key", "value2")))

	// Create a Float64Histogram
	histogram, err := meter.Float64Histogram("test.histogram")
	if err != nil {
		t.Fatalf("Failed to create histogram: %v", err)
	}

	histogram.Record(ctx, 123.45, metric.WithAttributes(attribute.String("endpoint", "/api/test")))

	// Create a Gauge
	gauge, err := meter.Float64Gauge("test.gauge")
	if err != nil {
		t.Fatalf("Failed to create gauge: %v", err)
	}

	gauge.Record(ctx, 75.5, metric.WithAttributes(attribute.String("unit", "percent")))

	t.Log("Successfully created and used OTel instruments")
}
