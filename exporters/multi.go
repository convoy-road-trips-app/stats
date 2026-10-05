package exporters

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/convoy-road-trips-app/stats/models"
)

// multi fans one batch out to several child exporters.
type multi struct {
	name           string
	defaultTimeout time.Duration
	children       []models.Exporter
	maxTimeout     time.Duration
}

// Multi returns an exporter named name that forwards every call to es.
//
// Children run concurrently, and each one is bounded independently: a child
// that implements models.ExportTimeouter gets context.WithTimeout(ctx, its
// ExportTimeout()), every other child gets defaultTimeout. A defaultTimeout of
// zero or less leaves children without their own timeout unbounded by Multi.
// Nil children are ignored.
//
// Capabilities follow the wrapper rules of the pipeline, where the presence of
// ExportTimeout turns off the pipeline's default export deadline:
//   - Multi always implements models.ExportTimeouter. It reports the largest
//     child timeout, using defaultTimeout for children without one, so the
//     pipeline does not cut short a child that asked for more time. Multi
//     applies the per-child bounds itself.
//   - Multi always implements models.IdleExporter. ExportIdle is forwarded
//     only to children that implement it, each bounded the same way.
//
// A panic in one child is recovered and reported as that child's error; it
// does not stop its siblings. Child errors are prefixed with the child's name
// and combined with errors.Join, so errors.Is still matches the originals.
// Shutdown fans out to every child with the caller's context and joins the
// errors.
//
// Like every exporter, children share the batch and must not modify it.
func Multi(name string, defaultTimeout time.Duration, es ...models.Exporter) models.Exporter {
	m := &multi{name: name, defaultTimeout: defaultTimeout}
	for _, e := range es {
		if e == nil {
			continue
		}
		m.children = append(m.children, e)
		if d := m.timeoutFor(e); d > m.maxTimeout {
			m.maxTimeout = d
		}
	}
	return m
}

// Name returns the name given to Multi.
func (m *multi) Name() string { return m.name }

// ExportTimeout returns the largest timeout among the children.
func (m *multi) ExportTimeout() time.Duration { return m.maxTimeout }

func (m *multi) timeoutFor(e models.Exporter) time.Duration {
	if t, ok := e.(models.ExportTimeouter); ok {
		return t.ExportTimeout()
	}
	return m.defaultTimeout
}

// Export sends metrics to every child concurrently.
func (m *multi) Export(ctx context.Context, metrics []*models.Metric) error {
	return m.fanOut(ctx, true, nil, func(ctx context.Context, e models.Exporter) error {
		return e.Export(ctx, metrics)
	})
}

// ExportIdle forwards to the children that implement models.IdleExporter.
func (m *multi) ExportIdle(ctx context.Context) error {
	return m.fanOut(ctx, true, func(e models.Exporter) bool {
		_, ok := e.(models.IdleExporter)
		return ok
	}, func(ctx context.Context, e models.Exporter) error {
		return e.(models.IdleExporter).ExportIdle(ctx)
	})
}

// Shutdown shuts every child down with the caller's context.
func (m *multi) Shutdown(ctx context.Context) error {
	return m.fanOut(ctx, false, nil, func(ctx context.Context, e models.Exporter) error {
		return e.Shutdown(ctx)
	})
}

// fanOut runs fn for each selected child in its own goroutine, optionally
// bounded per child, recovering panics, and joins the errors in child order.
func (m *multi) fanOut(
	ctx context.Context,
	bounded bool,
	selected func(models.Exporter) bool,
	fn func(context.Context, models.Exporter) error,
) error {
	errs := make([]error, len(m.children))
	var wg sync.WaitGroup
	for i, e := range m.children {
		if selected != nil && !selected(e) {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					errs[i] = fmt.Errorf("%s: panic: %v", e.Name(), r)
				}
			}()
			cctx := ctx
			if bounded {
				if d := m.timeoutFor(e); d > 0 {
					var cancel context.CancelFunc
					cctx, cancel = context.WithTimeout(ctx, d)
					defer cancel()
				}
			}
			if err := fn(cctx, e); err != nil {
				errs[i] = fmt.Errorf("%s: %w", e.Name(), err)
			}
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}
