package otlp

import (
	"context"
	"testing"
	"time"

	"github.com/convoy-road-trips-app/stats/models"
	"github.com/stretchr/testify/require"
)

func TestExporter_keeps_description_and_unit_of_merged_sums_and_histograms(t *testing.T) {
	// Given: several observations per metric, as one batch of a worker holds them
	start := time.Unix(100, 0)
	observation := func(name string, kind models.MetricType) *models.Metric {
		return &models.Metric{
			Name: name, Type: kind, Value: 1, Timestamp: start,
			Description: name + " description", Unit: name + "_unit",
		}
	}
	collector := &collectingExporter{}
	exporter := &Exporter{config: &models.OTLPConfig{Enabled: true}, otlpExporter: collector}

	// When
	require.NoError(t, exporter.Export(context.Background(), []*models.Metric{
		observation("requests_total", models.MetricTypeCounter),
		observation("requests_total", models.MetricTypeCounter),
		observation("duration_seconds", models.MetricTypeHistogram),
		observation("duration_seconds", models.MetricTypeHistogram),
		observation("queue_depth", models.MetricTypeGauge),
	}))

	// Then
	for _, name := range []string{"requests_total", "duration_seconds", "queue_depth"} {
		m := metricByName(t, collector.collections[0], name)
		require.Equal(t, name+" description", m.Description)
		require.Equal(t, name+"_unit", m.Unit)
	}
}
