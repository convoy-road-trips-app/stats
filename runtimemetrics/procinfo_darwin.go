//go:build darwin

package runtimemetrics

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// collectProcInfo reads the calling process from getrusage(2) and
// getrlimit(2). Darwin offers no cgo-free way to inspect another process, so
// any other pid fails with an error for which IsUnsupported is true.
func collectProcInfo(pid int) (ProcInfo, error) {
	if pid != os.Getpid() {
		return ProcInfo{}, fmt.Errorf("runtimemetrics: CollectProcInfo: only the current process can be read on darwin: %w", errors.ErrUnsupported)
	}
	var ru syscall.Rusage
	if err := readSelfRusage(&ru); err != nil {
		return ProcInfo{}, err
	}

	info := ProcInfo{
		CPU: CPUInfo{
			User: time.Duration(timevalSeconds(ru.Utime) * float64(time.Second)),
			Sys:  time.Duration(timevalSeconds(ru.Stime) * float64(time.Second)),
		},
		// ru_maxrss is the peak resident size, in bytes on Darwin.
		Memory: MemoryInfo{
			Resident:        nonNegative(ru.Maxrss),
			MajorPageFaults: nonNegative(ru.Majflt),
			MinorPageFaults: nonNegative(ru.Minflt),
		},
		Threads: ThreadInfo{
			VoluntaryContextSwitches:   nonNegative(ru.Nvcsw),
			InvoluntaryContextSwitches: nonNegative(ru.Nivcsw),
		},
	}

	var rl syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rl); err == nil && rl.Cur != syscall.RLIM_INFINITY {
		info.Files.Max = rl.Cur
	}
	return info, nil
}

// nonNegative converts a rusage counter to uint64, clamping negatives to zero.
func nonNegative(v int64) uint64 {
	if v < 0 {
		return 0
	}
	return uint64(v)
}
