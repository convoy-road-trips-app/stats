package stats_test

import (
	"github.com/convoy-road-trips-app/stats"
	"github.com/convoy-road-trips-app/stats/exporters/cloudwatch"
	"github.com/convoy-road-trips-app/stats/exporters/datadog"
	"github.com/convoy-road-trips-app/stats/exporters/otlp"
	"github.com/convoy-road-trips-app/stats/exporters/prometheus"
	"github.com/convoy-road-trips-app/stats/models"
)

// Compile-time checks that every built-in exporter satisfies models.Exporter
// and that the OTLP exporter implements both optional interfaces.
var (
	_ models.Exporter = (*datadog.Exporter)(nil)
	_ models.Exporter = (*prometheus.Exporter)(nil)
	_ models.Exporter = (*cloudwatch.Exporter)(nil)
	_ models.Exporter = (*otlp.Exporter)(nil)

	_ models.ExportTimeouter = (*otlp.Exporter)(nil)
	_ models.IdleExporter    = (*otlp.Exporter)(nil)

	_ stats.Exporter = models.Exporter(nil)
)
