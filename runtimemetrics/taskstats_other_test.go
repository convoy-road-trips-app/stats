//go:build !linux

package runtimemetrics

import (
	"errors"
	"testing"
)

func TestGetUnsupported(t *testing.T) {
	info, err := Get(1)
	if info != (DelayInfo{}) {
		t.Fatalf("info = %+v, want zero", info)
	}
	if !errors.Is(err, errTaskstatsUnsupported) || !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("err = %v, want errTaskstatsUnsupported/ErrUnsupported", err)
	}
}
