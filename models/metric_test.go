package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func TestMetric_Reset_clears_description_and_unit_before_pool_reuse(t *testing.T) {
	// Given
	m := &Metric{Name: "queue_depth", Description: "jobs waiting", Unit: "{job}"}

	// When
	m.Reset()

	// Then
	if m.Description != "" || m.Unit != "" {
		t.Fatalf("Reset kept metadata: description %q, unit %q", m.Description, m.Unit)
	}
}

func TestMetricCloneIsIndependent(t *testing.T) {
	orig := &Metric{
		Name:        "m",
		Type:        MetricTypeGauge,
		Value:       2,
		Attributes:  []attribute.KeyValue{attribute.String("k", "v")},
		Timestamp:   time.Unix(5, 0),
		Priority:    3,
		TraceID:     trace.TraceID{1},
		SpanID:      trace.SpanID{2},
		Description: "d",
		Unit:        "s",
	}

	clone := orig.Clone()
	require.Equal(t, orig, clone)
	require.NotSame(t, orig, clone)

	clone.Attributes[0] = attribute.String("k", "changed")
	clone.Name = "other"
	require.Equal(t, "v", orig.Attributes[0].Value.AsString())
	require.Equal(t, "m", orig.Name)
}

func TestMetricCloneNil(t *testing.T) {
	var m *Metric
	require.Nil(t, m.Clone())
}

func TestMetricCloneKeepsEmptyAttributesNil(t *testing.T) {
	require.Nil(t, (&Metric{Name: "m"}).Clone().Attributes)
}
