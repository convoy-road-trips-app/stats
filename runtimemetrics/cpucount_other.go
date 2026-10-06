//go:build !linux && !darwin

package runtimemetrics

// physicalCPUCount returns 0: the physical core count is only read on Linux
// and Darwin.
func physicalCPUCount() int { return 0 }
