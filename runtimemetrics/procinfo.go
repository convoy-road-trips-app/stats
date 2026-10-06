package runtimemetrics

import "time"

// ProcInfo is a point-in-time snapshot of a process, the counterpart of
// segmentio's procstats.ProcInfo. Cumulative values (times, faults, context
// switches) are totals since process start; subtract two snapshots for a rate.
type ProcInfo struct {
	CPU     CPUInfo
	Memory  MemoryInfo
	Files   FileInfo
	Threads ThreadInfo
}

// CPUInfo holds CPU time and the cgroup CPU configuration of a process.
type CPUInfo struct {
	User time.Duration // user CPU time used by the process
	Sys  time.Duration // system CPU time used by the process

	// Linux cgroup configuration; zero when unknown or not limited. See
	// procfs.CPUConfig.
	Period time.Duration // bandwidth enforcement period
	Quota  time.Duration // CPU time allowed per Period
	Shares int64         // cgroup v1 cpu.shares
	Weight int64         // cgroup v2 cpu.weight
}

// MemoryInfo holds memory usage and capacity of a process, in bytes.
type MemoryInfo struct {
	// Total is the memory the process may use: the cgroup limit when one is set,
	// otherwise the host's physical memory. Zero when unknown.
	Total uint64
	// Available is the host's MemAvailable. Zero when unknown.
	Available uint64
	// Size is the virtual size of the process (including mappings).
	Size     uint64
	Resident uint64 // resident set size
	Shared   uint64 // resident shared pages (file backed)
	Text     uint64 // code
	Data     uint64 // data and stack

	MajorPageFaults uint64
	MinorPageFaults uint64
}

// FileInfo holds file descriptor usage of a process.
type FileInfo struct {
	Open uint64 // open file descriptors
	// Max is the soft descriptor limit; zero when unlimited or unknown.
	Max uint64
}

// ThreadInfo holds the thread count and context switches of a process.
type ThreadInfo struct {
	Num                        uint64
	VoluntaryContextSwitches   uint64
	InvoluntaryContextSwitches uint64
}

// CollectProcInfo returns a snapshot of the process with the given pid, which
// may be a process other than the caller. It is the PID-addressable
// counterpart of the self-process metrics enabled with ProcessMetrics.
//
// On Linux it reads /proc (see the procfs package). Reading another user's
// process may fail on files restricted to its owner; only stat, statm and
// status are required, the remaining sources (limits, fd count, meminfo,
// cgroup) are best effort and leave their fields zero when unreadable.
//
// On Darwin only the calling process can be read (from getrusage, with no
// cgo); any other pid fails. On other platforms it always fails. In both
// failing cases IsUnsupported(err) is true.
func CollectProcInfo(pid int) (ProcInfo, error) {
	return collectProcInfo(pid)
}
