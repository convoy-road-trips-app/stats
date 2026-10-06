package stats

import (
	"context"
	"os"
	"runtime"
	"strings"

	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats/version"
)

const (
	// envDisableVersionReporting disables version reporting (both stats_version
	// and go_version) when set to true, TRUE, yes, 1 or on. WithVersionReporting
	// takes precedence over it.
	envDisableVersionReporting = "STATS_DISABLE_GO_VERSION_REPORTING"

	statsVersionMetric = "stats_version"
	goVersionMetric    = "go_version"
)

// WithVersionReporting turns the one-time version report on or off. It is on by
// default: the first successful record on the root client also records the
// gauges stats_version and go_version (value 1, the version in an attribute of
// the same name), tagged with the service and environment only. Setting
// STATS_DISABLE_GO_VERSION_REPORTING to true, TRUE, yes, 1 or on disables both
// gauges; an explicit WithVersionReporting, true or false, wins over it.
func WithVersionReporting(enabled bool) Option {
	return func(c *Config) {
		c.VersionReporting = &enabled
	}
}

// versionReportingEnabled resolves the option, then the environment, then the
// default (on).
func versionReportingEnabled(cfg *Config) bool {
	if cfg.VersionReporting != nil {
		return *cfg.VersionReporting
	}
	switch strings.TrimSpace(os.Getenv(envDisableVersionReporting)) {
	case "true", "TRUE", "yes", "1", "on":
		return false
	}
	return true
}

// reportVersionsOnce records the version gauges the first time it is called,
// when reporting is enabled. The caller holds core.mu.RLock. The gauges go
// straight to the pipeline with only the service-level attributes, so they never
// carry view or context tags, and a failed record is dropped like any other.
func (core *clientCore) reportVersionsOnce() {
	if !core.reportVersions {
		return
	}
	core.versionOnce.Do(func() {
		service := []attribute.KeyValue{
			attribute.String("service", core.cfg.ServiceName),
			attribute.String("environment", core.cfg.Environment),
		}
		core.recordVersionGauge(statsVersionMetric, version.Version(), service)
		if !version.DevelGoVersion() {
			core.recordVersionGauge(goVersionMetric, runtime.Version(), service)
		}
	})
}

// recordVersionGauge records a gauge of value 1 named name, tagged with the
// service attributes and name=version.
func (core *clientCore) recordVersionGauge(name, version string, service []attribute.KeyValue) {
	m := AcquireMetric()
	m.Name = name
	m.Type = MetricTypeGauge
	m.Value = 1
	m.Attributes = append(m.Attributes, service...)
	m.Attributes = append(m.Attributes, attribute.String(name, version))
	if err := core.pipeline.Record(context.Background(), m); err != nil {
		ReleaseMetric(m)
	}
}
