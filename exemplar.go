package stats

import (
	"context"

	"go.opentelemetry.io/otel/trace"
)

// attachExemplar records the span of ctx on counter and histogram observations
// when that span is sampled, so exporters can emit it as an exemplar (the OTel
// trace-based exemplar filter). Gauges, unsampled and span-less contexts leave
// m unchanged.
func attachExemplar(ctx context.Context, m *Metric) {
	switch m.Type {
	case MetricTypeCounter, MetricTypeHistogram:
	default: // gauges are last-value points without exemplars
		return
	}
	span := trace.SpanContextFromContext(ctx)
	if !span.IsSampled() || !span.IsValid() {
		return
	}
	m.TraceID = span.TraceID()
	m.SpanID = span.SpanID()
}
