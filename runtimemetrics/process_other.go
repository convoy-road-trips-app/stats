//go:build !linux

package runtimemetrics

// newProcessSource returns nil: process metrics are only collected on Linux,
// so the collector emits nothing here.
func newProcessSource() *processSource { return nil }
