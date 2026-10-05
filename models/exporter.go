package models

import (
	"context"
	"time"
)

// Exporter is the interface for backend exporters. Exporters receive batches
// that are shared with every other exporter running in parallel, so Export must
// not modify the metrics it is given.
type Exporter interface {
	Name() string
	Export(ctx context.Context, metrics []*Metric) error
	Shutdown(ctx context.Context) error
}

// ExportTimeouter is implemented by exporters that bound their own exports and
// must not inherit the pipeline's default export deadline. The pipeline detects
// it by the presence of the method.
type ExportTimeouter interface {
	ExportTimeout() time.Duration
}

// IdleExporter is implemented by exporters with cumulative state that must be
// exported on every flush interval, even when nothing was observed in it.
type IdleExporter interface {
	ExportIdle(ctx context.Context) error
}
