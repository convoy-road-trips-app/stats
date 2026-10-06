//go:build linux

package runtimemetrics

import (
	"os"

	"github.com/convoy-road-trips-app/stats/runtimemetrics/procfs"
)

func collectProcInfo(pid int) (ProcInfo, error) {
	pageSize := os.Getpagesize()
	if pageSize <= 0 {
		pageSize = 4096
	}
	return collectProcInfoFrom(procfs.Default, pid, uint64(pageSize))
}
