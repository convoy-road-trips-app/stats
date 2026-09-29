package stats

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/convoy-road-trips-app/stats/exporters/cloudwatch"
	"github.com/convoy-road-trips-app/stats/exporters/datadog"
	"github.com/convoy-road-trips-app/stats/exporters/otlp"
	"github.com/convoy-road-trips-app/stats/exporters/prometheus"
	"github.com/convoy-road-trips-app/stats/transport"
)

func NewPipeline(cfg *Config) (*Pipeline, error) {
	if cfg == nil {
		return nil, fmt.Errorf("%w: config is nil", ErrInvalidConfig)
	}
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}

	exporters := make([]Exporter, 0, 3)
	if cfg.Datadog != nil && cfg.Datadog.Enabled {
		ddExporter, err := datadog.NewExporter(cfg.Datadog)
		if err != nil {
			return nil, fmt.Errorf("create datadog exporter: %w", err)
		}
		exporters = append(exporters, ddExporter)
	}
	if cfg.Prometheus != nil && cfg.Prometheus.Enabled {
		promExporter, err := prometheus.NewExporter(cfg.Prometheus)
		if err != nil {
			return nil, fmt.Errorf("create prometheus exporter: %w", err)
		}
		exporters = append(exporters, promExporter)
	}
	if cfg.CloudWatch != nil && cfg.CloudWatch.Enabled {
		cwExporter, err := cloudwatch.NewExporter(cfg.CloudWatch)
		if err != nil {
			return nil, fmt.Errorf("create cloudwatch exporter: %w", err)
		}
		exporters = append(exporters, cwExporter)
	}
	if cfg.OTLP != nil && cfg.OTLP.Enabled {
		// Stats-level placeholders are not explicit identity: leave them unset so
		// OTEL_SERVICE_NAME / DEPLOYMENT_ENVIRONMENT (and spec fallbacks) apply.
		defaults := DefaultConfig()
		if cfg.OTLP.ServiceName == "" && cfg.ServiceName != defaults.ServiceName {
			cfg.OTLP.ServiceName = cfg.ServiceName
		}
		if cfg.OTLP.DeploymentEnvironment == "" && cfg.Environment != defaults.Environment {
			cfg.OTLP.DeploymentEnvironment = cfg.Environment
		}
		otlpExporter, err := otlp.NewExporter(cfg.OTLP)
		if err != nil {
			return nil, fmt.Errorf("create otlp exporter: %w", err)
		}
		exporters = append(exporters, otlpExporter)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var rateLimiter *RateLimiter
	if cfg.RateLimitPerSecond > 0 {
		rateLimiter = NewRateLimiter(cfg.RateLimitPerSecond, cfg.RateLimitBurst)
	}

	return &Pipeline{
		cfg:            cfg,
		buffer:         transport.NewRingBuffer(cfg.BufferSize),
		workers:        cfg.Workers,
		exporters:      exporters,
		exporterErrors: make([]atomic.Uint64, len(exporters)),
		rateLimiter:    rateLimiter,
		ctx:            ctx,
		cancel:         cancel,
		shutdownCh:     make(chan struct{}),
	}, nil
}
