package otlp

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/convoy-road-trips-app/stats/models"
)

func TestNewExporter(t *testing.T) {
	tests := []struct {
		name        string
		config      *models.OTLPConfig
		expectError bool
	}{
		{
			name:        "nil config",
			config:      nil,
			expectError: true,
		},
		{
			name: "disabled config",
			config: &models.OTLPConfig{
				Enabled: false,
			},
			expectError: false,
		},
		{
			name: "missing endpoint",
			config: &models.OTLPConfig{
				Enabled: true,
			},
			expectError: true,
		},
		{
			name: "valid config",
			config: &models.OTLPConfig{
				Enabled:  true,
				Endpoint: "localhost:4317",
				Insecure: true,
				Headers:  map[string]string{"foo": "bar"},
			},
			expectError: false,
		},
		{
			name: "valid http config",
			config: &models.OTLPConfig{
				Enabled:  true,
				Endpoint: "localhost:4318",
				Insecure: true,
				Protocol: models.OTLPProtocolHTTP,
			},
			expectError: false,
		},
		{
			name: "invalid protocol",
			config: &models.OTLPConfig{
				Enabled:  true,
				Endpoint: "localhost:4317",
				Protocol: "websocket",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exp, err := NewExporter(tt.config)
			if tt.expectError {
				require.Error(t, err)
				assert.Nil(t, exp)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, exp)
			}
		})
	}
}

func TestToResourceMetrics(t *testing.T) {
	now := time.Now()
	metrics := []*models.Metric{
		{
			Name:  "test_counter",
			Type:  models.MetricTypeCounter,
			Value: 42,
			Attributes: []attribute.KeyValue{
				attribute.String("env", "test"),
			},
			Timestamp: now,
		},
		{
			Name:  "test_gauge",
			Type:  models.MetricTypeGauge,
			Value: 3.14,
			Attributes: []attribute.KeyValue{
				attribute.String("env", "test"),
			},
			Timestamp: now,
		},
		{
			Name:  "test_histogram",
			Type:  models.MetricTypeHistogram,
			Value: 123,
			Attributes: []attribute.KeyValue{
				attribute.String("env", "test"),
			},
			Timestamp: now,
		},
	}

	rm := toResourceMetrics("my-service", metrics)

	// Verify Resource
	assert.NotNil(t, rm.Resource)
	attrs := rm.Resource.Attributes()
	assert.Len(t, attrs, 1)
	assert.Equal(t, attribute.String("service.name", "my-service"), attrs[0])

	// Verify ScopeMetrics
	assert.Len(t, rm.ScopeMetrics, 1)
	sm := rm.ScopeMetrics[0]
	assert.Equal(t, "github.com/convoy-road-trips-app/stats", sm.Scope.Name)

	// Verify Metrics
	assert.Len(t, sm.Metrics, 3)

	// Verify Counter
	counter := sm.Metrics[0]
	assert.Equal(t, "test_counter", counter.Name)
	sum, ok := counter.Data.(metricdata.Sum[float64])
	assert.True(t, ok)
	assert.Equal(t, metricdata.DeltaTemporality, sum.Temporality)
	assert.True(t, sum.IsMonotonic)
	assert.Len(t, sum.DataPoints, 1)
	assert.InDelta(t, 42.0, sum.DataPoints[0].Value, 0.001)
	assert.Equal(t, now, sum.DataPoints[0].Time)
	assert.True(t, sum.DataPoints[0].Attributes.HasValue("env"))

	// Verify Gauge
	gauge := sm.Metrics[1]
	assert.Equal(t, "test_gauge", gauge.Name)
	g, ok := gauge.Data.(metricdata.Gauge[float64])
	assert.True(t, ok)
	assert.Len(t, g.DataPoints, 1)
	assert.InDelta(t, 3.14, g.DataPoints[0].Value, 0.001)
	assert.Equal(t, now, g.DataPoints[0].Time)
	assert.True(t, g.DataPoints[0].Attributes.HasValue("env"))

	// Verify Histogram
	hist := sm.Metrics[2]
	assert.Equal(t, "test_histogram", hist.Name)
	h, ok := hist.Data.(metricdata.Histogram[float64])
	assert.True(t, ok)
	assert.Equal(t, metricdata.DeltaTemporality, h.Temporality)
	assert.Len(t, h.DataPoints, 1)
	assert.Equal(t, uint64(1), h.DataPoints[0].Count)
	assert.InDelta(t, 123.0, h.DataPoints[0].Sum, 0.001)
	assert.Equal(t, now, h.DataPoints[0].Time)
	assert.True(t, h.DataPoints[0].Attributes.HasValue("env"))
}

func TestExporter_Name(t *testing.T) {
	exp := &Exporter{}
	assert.Equal(t, "otlp", exp.Name())
}

func TestExporter_Export_EmptyMetrics(t *testing.T) {
	exp := &Exporter{
		config: &models.OTLPConfig{Enabled: true},
	}
	err := exp.Export(context.Background(), []*models.Metric{})
	assert.NoError(t, err)
}

func TestExporter_Export_Disabled(t *testing.T) {
	exp := &Exporter{
		config: &models.OTLPConfig{Enabled: false},
	}
	err := exp.Export(context.Background(), []*models.Metric{
		{Name: "test"},
	})
	assert.NoError(t, err)
}

func TestExporter_Export_NilMetrics(t *testing.T) {
	exp := &Exporter{
		config: &models.OTLPConfig{Enabled: true},
	}
	err := exp.Export(context.Background(), nil)
	assert.NoError(t, err)
}

func TestExporter_Shutdown(t *testing.T) {
	exp, err := NewExporter(&models.OTLPConfig{
		Enabled:  true,
		Endpoint: "localhost:4317",
		Insecure: true,
	})
	require.NoError(t, err)

	err = exp.Shutdown(context.Background())
	assert.NoError(t, err)
}

func TestExporter_Shutdown_CancelledContext(t *testing.T) {
	exp, err := NewExporter(&models.OTLPConfig{
		Enabled:  true,
		Endpoint: "localhost:4317",
		Insecure: true,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = exp.Shutdown(ctx)
	assert.Error(t, err)
}

func TestToResourceMetrics_EmptyServiceName(t *testing.T) {
	now := time.Now()
	metrics := []*models.Metric{
		{
			Name:      "test",
			Type:      models.MetricTypeCounter,
			Value:     1,
			Timestamp: now,
		},
	}

	rm := toResourceMetrics("", metrics)

	attrs := rm.Resource.Attributes()
	assert.Len(t, attrs, 1)
	assert.Equal(t, attribute.String("service.name", "unknown_service"), attrs[0])
}

func TestToResourceMetrics_EmptyMetrics(t *testing.T) {
	rm := toResourceMetrics("svc", []*models.Metric{})

	assert.NotNil(t, rm.Resource)
	assert.Len(t, rm.ScopeMetrics, 1)
	assert.Empty(t, rm.ScopeMetrics[0].Metrics)
}

func TestToResourceMetrics_NoAttributes(t *testing.T) {
	now := time.Now()
	metrics := []*models.Metric{
		{
			Name:       "no_attrs",
			Type:       models.MetricTypeGauge,
			Value:      99.9,
			Attributes: nil,
			Timestamp:  now,
		},
	}

	rm := toResourceMetrics("svc", metrics)
	sm := rm.ScopeMetrics[0]
	assert.Len(t, sm.Metrics, 1)

	g, ok := sm.Metrics[0].Data.(metricdata.Gauge[float64])
	assert.True(t, ok)
	assert.Len(t, g.DataPoints, 1)
	assert.Equal(t, 0, g.DataPoints[0].Attributes.Len())
}

func TestToResourceMetrics_MultipleAttributes(t *testing.T) {
	now := time.Now()
	metrics := []*models.Metric{
		{
			Name:  "multi_attr",
			Type:  models.MetricTypeCounter,
			Value: 5,
			Attributes: []attribute.KeyValue{
				attribute.String("env", "prod"),
				attribute.Int64("code", 200),
				attribute.Bool("cached", true),
			},
			Timestamp: now,
		},
	}

	rm := toResourceMetrics("svc", metrics)
	sm := rm.ScopeMetrics[0]
	sum, ok := sm.Metrics[0].Data.(metricdata.Sum[float64])
	assert.True(t, ok)
	assert.Equal(t, 3, sum.DataPoints[0].Attributes.Len())
	assert.True(t, sum.DataPoints[0].Attributes.HasValue("env"))
	assert.True(t, sum.DataPoints[0].Attributes.HasValue("code"))
	assert.True(t, sum.DataPoints[0].Attributes.HasValue("cached"))
}

func TestToResourceMetrics_UnknownMetricType(t *testing.T) {
	now := time.Now()
	metrics := []*models.Metric{
		{
			Name:      "unknown",
			Type:      models.MetricType(99),
			Value:     1,
			Timestamp: now,
		},
	}

	rm := toResourceMetrics("svc", metrics)
	sm := rm.ScopeMetrics[0]
	assert.Empty(t, sm.Metrics)
}

func TestToResourceMetrics_LargeMetricBatch(t *testing.T) {
	now := time.Now()
	metrics := make([]*models.Metric, 100)
	for i := range metrics {
		metrics[i] = &models.Metric{
			Name:      fmt.Sprintf("metric_%d", i),
			Type:      models.MetricType(i % 3),
			Value:     float64(i),
			Timestamp: now,
			Attributes: []attribute.KeyValue{
				attribute.Int64("index", int64(i)),
			},
		}
	}

	rm := toResourceMetrics("batch-svc", metrics)
	sm := rm.ScopeMetrics[0]
	assert.Len(t, sm.Metrics, 100)

	for i, m := range sm.Metrics {
		assert.Equal(t, fmt.Sprintf("metric_%d", i), m.Name)
		switch i % 3 {
		case 0:
			_, ok := m.Data.(metricdata.Sum[float64])
			assert.True(t, ok, "metric %d should be Sum", i)
		case 1:
			_, ok := m.Data.(metricdata.Gauge[float64])
			assert.True(t, ok, "metric %d should be Gauge", i)
		case 2:
			_, ok := m.Data.(metricdata.Histogram[float64])
			assert.True(t, ok, "metric %d should be Histogram", i)
		}
	}
}

func TestToResourceMetrics_CounterProperties(t *testing.T) {
	now := time.Now()
	metrics := []*models.Metric{
		{
			Name:      "counter",
			Type:      models.MetricTypeCounter,
			Value:     7.5,
			Timestamp: now,
		},
	}

	rm := toResourceMetrics("svc", metrics)
	sum := rm.ScopeMetrics[0].Metrics[0].Data.(metricdata.Sum[float64])

	assert.True(t, sum.IsMonotonic, "counters should be monotonic")
	assert.Equal(t, metricdata.DeltaTemporality, sum.Temporality)
	assert.InDelta(t, 7.5, sum.DataPoints[0].Value, 0.001)
	assert.Equal(t, now, sum.DataPoints[0].Time)
}

func TestToResourceMetrics_HistogramProperties(t *testing.T) {
	now := time.Now()
	metrics := []*models.Metric{
		{
			Name:      "hist",
			Type:      models.MetricTypeHistogram,
			Value:     250.5,
			Timestamp: now,
		},
	}

	rm := toResourceMetrics("svc", metrics)
	h := rm.ScopeMetrics[0].Metrics[0].Data.(metricdata.Histogram[float64])

	assert.Equal(t, metricdata.DeltaTemporality, h.Temporality)
	dp := h.DataPoints[0]
	assert.Equal(t, uint64(1), dp.Count)
	assert.InDelta(t, 250.5, dp.Sum, 0.001)
	assert.Equal(t, now, dp.Time)
	assert.Empty(t, dp.Bounds)
	assert.Equal(t, []uint64{1}, dp.BucketCounts)
}

func TestNewExporter_ConfigValidation(t *testing.T) {
	tests := []struct {
		name        string
		config      *models.OTLPConfig
		expectError bool
		errorMsg    string
	}{
		{
			name:        "nil config returns error",
			config:      nil,
			expectError: true,
			errorMsg:    "otlp config is nil",
		},
		{
			name: "enabled without endpoint returns error",
			config: &models.OTLPConfig{
				Enabled: true,
			},
			expectError: true,
			errorMsg:    "endpoint is required",
		},
		{
			name: "disabled without endpoint is valid",
			config: &models.OTLPConfig{
				Enabled:  false,
				Endpoint: "",
			},
			expectError: false,
		},
		{
			name: "valid with headers",
			config: &models.OTLPConfig{
				Enabled:  true,
				Endpoint: "collector.example.com:4317",
				Headers: map[string]string{
					"Authorization": "Bearer token",
					"X-Custom":      "value",
				},
			},
			expectError: false,
		},
		{
			name: "valid with service name",
			config: &models.OTLPConfig{
				Enabled:     true,
				Endpoint:    "localhost:4317",
				Insecure:    true,
				ServiceName: "my-service",
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exp, err := NewExporter(tt.config)
			if tt.expectError {
				require.Error(t, err)
				assert.Nil(t, exp)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				require.NoError(t, err)
				assert.NotNil(t, exp)
			}
		})
	}
}

func TestOTLPConfig_Validate(t *testing.T) {
	tests := []struct {
		name        string
		config      models.OTLPConfig
		expectError bool
	}{
		{
			name:        "disabled is always valid",
			config:      models.OTLPConfig{Enabled: false},
			expectError: false,
		},
		{
			name:        "enabled with endpoint is valid",
			config:      models.OTLPConfig{Enabled: true, Endpoint: "localhost:4317"},
			expectError: false,
		},
		{
			name:        "enabled without endpoint is invalid",
			config:      models.OTLPConfig{Enabled: true, Endpoint: ""},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestOTLPConfig_Address(t *testing.T) {
	config := models.OTLPConfig{
		Endpoint: "collector.internal:4317",
	}
	assert.Equal(t, "collector.internal:4317", config.Address())
}
