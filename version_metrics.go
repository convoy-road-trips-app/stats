package stats

import (
	"context"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"

	"go.opentelemetry.io/otel/attribute"
)

const (
	// envDisableVersionReporting disables version reporting (both stats_version
	// and go_version) when set to true, TRUE, yes or 1. WithVersionReporting
	// takes precedence over it.
	envDisableVersionReporting = "STATS_DISABLE_GO_VERSION_REPORTING"

	// statsModulePath is the module whose version stats_version reports.
	statsModulePath = "github.com/convoy-road-trips-app/stats"

	statsVersionMetric = "stats_version"
	goVersionMetric    = "go_version"
)

// WithVersionReporting turns the one-time version report on or off. It is on by
// default: the first successful record on the root client also records the
// gauges stats_version and go_version (value 1, the version in an attribute of
// the same name), tagged with the service and environment only. Setting
// STATS_DISABLE_GO_VERSION_REPORTING to true, TRUE, yes or 1 disables both
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
	case "true", "TRUE", "yes", "1":
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
		core.recordVersionGauge(statsVersionMetric, statsVersion(), service)
		if goVer := runtime.Version(); !strings.HasPrefix(goVer, "devel") {
			core.recordVersionGauge(goVersionMetric, goVer, service)
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

var (
	statsVersionOnce  sync.Once
	statsVersionValue string
)

// statsVersion returns the module version of this library from the build info,
// or "(devel)" when it is unknown, as for a local build of the module itself.
func statsVersion() string {
	statsVersionOnce.Do(func() {
		statsVersionValue = "(devel)"
		info, ok := debug.ReadBuildInfo()
		if !ok {
			return
		}
		if info.Main.Path == statsModulePath && info.Main.Version != "" {
			statsVersionValue = info.Main.Version
		}
		for _, dep := range info.Deps {
			if dep.Path == statsModulePath {
				if dep.Replace != nil && dep.Replace.Version != "" {
					statsVersionValue = dep.Replace.Version
				} else if dep.Version != "" {
					statsVersionValue = dep.Version
				}
				return
			}
		}
	})
	return statsVersionValue
}
