package otel

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/convoy-road-trips-app/stats"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
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

func TestWithResource_attachesResourceAttributesToOTLP(t *testing.T) {
	// Given
	res := resource.NewWithAttributes("", attribute.String("team", "payments"))
	provider, err := NewMeterProvider(WithResource(res))
	if err != nil {
		t.Fatalf("create MeterProvider: %v", err)
	}
	defer provider.Shutdown(context.Background())

	// When
	// Then
	config := stats.DefaultConfig()
	for _, option := range provider.clientOptions {
		option(config)
	}
	stats.WithOTLPResourceAttributes(provider.resource.Attributes()...)(config)
	attrs := attribute.NewSet(config.OTLP.ResourceAttributes...)
	if value, ok := attrs.Value("team"); !ok || value.AsString() != "payments" {
		t.Fatalf("expected team=payments in OTLP client resource configuration, got %v", attrs)
	}
}

func TestWithResource_mergesEnvironmentAndExplicitConfig(t *testing.T) {
	// Given
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "team=payments,service.name=resource-service,deployment.environment=resource-env,service.version=resource-version")
	t.Setenv("OTEL_SERVICE_NAME", "env-service")
	t.Setenv("DEPLOYMENT_ENVIRONMENT", "production")
	t.Setenv("SERVICE_VERSION", "2.4.1")
	res := resource.NewWithAttributes("",
		attribute.String("team", "explicit-team"),
		attribute.String("service.name", "explicit-service"),
		attribute.String("deployment.environment", "configured-env"),
		attribute.String("service.version", "1.0.0"),
	)
	provider, err := NewMeterProvider(WithResource(res))
	if err != nil {
		t.Fatalf("create MeterProvider: %v", err)
	}
	defer provider.Shutdown(context.Background())
	config := stats.DefaultConfig()
	for _, option := range provider.clientOptions {
		option(config)
	}
	stats.WithOTLPResourceAttributes(provider.resource.Attributes()...)(config)

	// When
	attributes := config.OTLP.ResourceAttributes
	resourceAttrs := make(map[string]string, len(attributes))
	for _, attr := range attributes {
		resourceAttrs[string(attr.Key)] = attr.Value.AsString()
	}

	// Then
	for key, want := range map[string]string{
		"service.name":           "explicit-service",
		"deployment.environment": "configured-env",
		"service.version":        "1.0.0",
		"team":                   "explicit-team",
	} {
		if got := resourceAttrs[key]; got != want {
			t.Errorf("resource attribute %q = %q, want %q", key, got, want)
		}
	}
}

func TestWithResource_survives_stats_options_regardless_of_order(t *testing.T) {
	// Given
	res := resource.NewWithAttributes("", attribute.String("team", "payments"))
	options := []struct {
		name string
		opts []MeterProviderOption
	}{
		{"resource first", []MeterProviderOption{WithResource(res), WithStatsOptions(stats.WithOTLP(&stats.OTLPConfig{Endpoint: "localhost:4317"}))}},
		{"stats first", []MeterProviderOption{WithStatsOptions(stats.WithOTLP(&stats.OTLPConfig{Endpoint: "localhost:4317"})), WithResource(res)}},
	}
	for _, tc := range options {
		t.Run(tc.name, func(t *testing.T) {
			// When
			provider, err := NewMeterProvider(tc.opts...)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
			config := stats.DefaultConfig()
			for _, option := range provider.clientOptions {
				option(config)
			}
			stats.WithOTLPResourceAttributes(provider.resource.Attributes()...)(config)

			// Then
			attrs := attribute.NewSet(config.OTLP.ResourceAttributes...)
			if value, ok := attrs.Value("team"); !ok || value.AsString() != "payments" {
				t.Fatalf("missing WithResource attribute: %v", attrs)
			}
		})
	}
}

func TestWithResource_is_exported_to_OTLP_receiver(t *testing.T) {
	// Given
	received := make(chan *collectormetricspb.ExportMetricsServiceRequest, 1)
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
	provider, err := NewMeterProvider(
		WithResource(resource.NewWithAttributes("", attribute.String("team", "payments"))),
		WithStatsOptions(stats.WithOTLP(&stats.OTLPConfig{
			Endpoint: strings.TrimPrefix(server.URL, "http://"),
			Insecure: true,
			Protocol: stats.OTLPProtocolHTTP,
		})),
	)
	if err != nil {
		t.Fatalf("create MeterProvider: %v", err)
	}
	counter, err := provider.Meter("test").Int64Counter("requests_total")
	if err != nil {
		t.Fatalf("create counter: %v", err)
	}

	// When
	counter.Add(context.Background(), 1)
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown MeterProvider: %v", err)
	}

	// Then
	request := <-received
	resourceAttributes := request.ResourceMetrics[0].Resource.Attributes
	found := false
	for _, attr := range resourceAttributes {
		if attr.Key == "team" && attr.Value.GetStringValue() == "payments" {
			found = true
		}
	}
	if !found {
		t.Fatalf("OTLP receiver did not receive WithResource attribute team=payments: %v", resourceAttributes)
	}
}

func TestWithResource_schema_URL_is_exported_to_OTLP_receiver(t *testing.T) {
	const schemaURL = "https://opentelemetry.io/schemas/1.26.0"
	for name, option := range map[string]MeterProviderOption{
		"WithResource": WithResource(resource.NewWithAttributes(schemaURL, attribute.String("team", "payments"))),
		"stats option beside a schemaless WithResource": func(mp *MeterProvider) error {
			return errors.Join(
				WithStatsOptions(stats.WithOTLPResourceSchemaURL(schemaURL))(mp),
				WithResource(resource.NewWithAttributes("", attribute.String("team", "payments")))(mp),
			)
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Given
			receiver := newMetricsReceiver(t)
			provider := receiver.provider(t, option)
			counter, err := provider.Meter("test").Int64Counter("requests_total")
			if err != nil {
				t.Fatalf("create counter: %v", err)
			}
			counter.Add(context.Background(), 1)

			// When
			if err := provider.ForceFlush(context.Background()); err != nil {
				t.Fatalf("force flush: %v", err)
			}

			// Then
			receiver.mu.Lock()
			defer receiver.mu.Unlock()
			if len(receiver.log) == 0 {
				t.Fatal("OTLP receiver got no export")
			}
			if got := receiver.log[0].ResourceMetrics[0].GetSchemaUrl(); got != schemaURL {
				t.Fatalf("ResourceMetrics.schema_url = %q, want %q", got, schemaURL)
			}
		})
	}
}
