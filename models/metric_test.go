package models

import "testing"

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
