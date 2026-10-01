package otlp

import "testing"

// requireComparable compiles only for comparable types.
func requireComparable[T comparable]() {}

func TestExporter_stays_comparable_as_in_v1_0(_ *testing.T) {
	// Given: v1.0.x exported a comparable Exporter struct
	// When: the package is compiled
	// Then: Exporter satisfies the comparable constraint
	requireComparable[Exporter]()
}
