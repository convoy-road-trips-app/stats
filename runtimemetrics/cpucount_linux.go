//go:build linux

package runtimemetrics

import "os"

// physicalCPUCount reads the physical core count from /proc/cpuinfo. It
// returns 0 when the topology is not exposed.
func physicalCPUCount() int {
	b, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return 0
	}
	n, _ := parseCPUInfoPhysical(b)
	return n
}
