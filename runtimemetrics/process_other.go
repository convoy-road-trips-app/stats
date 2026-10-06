//go:build !linux && !darwin

package runtimemetrics

// newPlatformProcessState returns nil: process metrics are only collected on
// Linux and Darwin, so the collector emits nothing on other platforms.
func newPlatformProcessState() *processState { return nil }
