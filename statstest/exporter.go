// Package statstest provides test helpers for code that records metrics with
// the stats package. Exporter captures what the pipeline exports so tests can
// assert on it, and NewClient wires one into a client.
//
// The package imports only stats, models and exporters, never internal/.
package statstest

import (
	"context"
	"slices"
	"sync"

	"github.com/convoy-road-trips-app/stats/models"
)

// exporterName is the name the Exporter reports to the pipeline, and the key of
// its entry in PipelineStats.ExporterErrors.
const exporterName = "statstest"

// Exporter is an in-memory models.Exporter that captures every metric it is
// given. It is safe for concurrent use.
//
// The pipeline reuses pooled metrics and shares each batch with every other
// exporter, so Exporter never keeps the pointers it receives: it stores deep
// copies, and Metrics returns deep copies again.
type Exporter struct {
	mu      sync.Mutex
	metrics []models.Metric
	exports int
}

var _ models.Exporter = (*Exporter)(nil)

// NewExporter returns an empty Exporter. Most tests want NewClient instead.
func NewExporter() *Exporter {
	return &Exporter{}
}

// Name returns "statstest". Register at most one Exporter per client, since
// exporter names must be unique.
func (e *Exporter) Name() string {
	return exporterName
}

// Export appends a deep copy of every metric in the batch to the capture and
// counts the call for FlushCalls. It never modifies or retains the batch. Nil
// entries are skipped.
func (e *Exporter) Export(_ context.Context, batch []*models.Metric) error {
	captured := make([]models.Metric, 0, len(batch))
	for _, m := range batch {
		if m != nil {
			captured = append(captured, copyMetric(m))
		}
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.exports++
	e.metrics = append(e.metrics, captured...)
	return nil
}

// Shutdown does nothing. The capture stays readable after the client closes.
func (e *Exporter) Shutdown(context.Context) error {
	return nil
}

// Metrics returns a deep copy of every metric captured since the last Clear,
// in export order. The result is the caller's to modify.
func (e *Exporter) Metrics() []models.Metric {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]models.Metric, len(e.metrics))
	for i := range e.metrics {
		out[i] = copyMetric(&e.metrics[i])
	}
	return out
}

// Clear discards the captured metrics and resets FlushCalls to zero.
func (e *Exporter) Clear() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.metrics = nil
	e.exports = 0
}

// FlushCalls returns the number of Export calls received since the last Clear.
//
// It counts every call the pipeline makes, whether it came from an explicit
// Client.Flush, from Close, or from a worker's batch-size or timer flush: the
// pipeline does not tell an exporter why it is exporting. The pipeline skips
// Export for an empty batch, so a Flush with nothing buffered adds no call.
// Tests that need an exact count should call Flush once after recording and
// compare against a value read before it, rather than assume the timer never
// fires.
func (e *Exporter) FlushCalls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.exports
}

// copyMetric returns a deep copy of m. attribute.KeyValue values are immutable,
// so cloning the slice is enough.
func copyMetric(m *models.Metric) models.Metric {
	c := *m
	c.Attributes = slices.Clone(m.Attributes)
	return c
}
