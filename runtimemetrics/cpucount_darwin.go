//go:build darwin

package runtimemetrics

import "syscall"

// physicalCPUCount reads hw.physicalcpu through sysctl (no cgo). It returns 0
// when the call fails.
func physicalCPUCount() int {
	n, err := syscall.SysctlUint32("hw.physicalcpu")
	if err != nil {
		return 0
	}
	return int(n)
}
