package exporters

import (
	"context"
	"time"

	"github.com/convoy-road-trips-app/stats/models"
)

// Filtered wraps e so that each batch is passed through filter before it
// reaches e.Export.
//
// Batches are shared with every other exporter running in parallel, so filter
// must not modify the metrics (or the batch slice) it is given: it must return
// a subset of the input or copies of the metrics. If filter returns an empty
// batch, e.Export is not called and Export returns nil. A nil filter passes
// batches through unchanged.
//
// The pipeline detects the optional ExportTimeout and ExportIdle capabilities
// by the presence of the methods, so the returned wrapper implements exactly
// the capabilities e has: none, ExportTimeout only, ExportIdle only, or both.
func Filtered(e models.Exporter, filter func([]*models.Metric) []*models.Metric) models.Exporter {
	base := filtered{inner: e, filter: filter}
	t, hasTimeout := e.(models.ExportTimeouter)
	i, hasIdle := e.(models.IdleExporter)
	switch {
	case hasTimeout && hasIdle:
		return &filteredBoundedIdle{filteredBounded: filteredBounded{filtered: base, timeouter: t}, idle: i}
	case hasTimeout:
		return &filteredBounded{filtered: base, timeouter: t}
	case hasIdle:
		return &filteredIdle{filtered: base, idle: i}
	default:
		return &base
	}
}

type filtered struct {
	inner  models.Exporter
	filter func([]*models.Metric) []*models.Metric
}

func (f *filtered) Name() string { return f.inner.Name() }

func (f *filtered) Export(ctx context.Context, metrics []*models.Metric) error {
	if f.filter != nil {
		metrics = f.filter(metrics)
	}
	if len(metrics) == 0 {
		return nil
	}
	return f.inner.Export(ctx, metrics)
}

func (f *filtered) Shutdown(ctx context.Context) error { return f.inner.Shutdown(ctx) }

type filteredBounded struct {
	filtered
	timeouter models.ExportTimeouter
}

func (f *filteredBounded) ExportTimeout() time.Duration { return f.timeouter.ExportTimeout() }

type filteredIdle struct {
	filtered
	idle models.IdleExporter
}

func (f *filteredIdle) ExportIdle(ctx context.Context) error { return f.idle.ExportIdle(ctx) }

type filteredBoundedIdle struct {
	filteredBounded
	idle models.IdleExporter
}

func (f *filteredBoundedIdle) ExportIdle(ctx context.Context) error { return f.idle.ExportIdle(ctx) }
