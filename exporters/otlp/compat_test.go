package otlp

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExporter_stays_comparable_as_in_v1_0(t *testing.T) {
	// Given: v1.0.x exported a comparable Exporter struct

	// When
	isComparable := reflect.TypeFor[Exporter]().Comparable()

	// Then
	require.True(t, isComparable)
}
