package runtimemetrics

import (
	"time"

	"github.com/convoy-road-trips-app/stats/runtimemetrics/procfs"
)

// collectProcInfoFrom builds a ProcInfo from procfs files read through r. It is
// independent of the host platform so it can run against fixtures.
func collectProcInfoFrom(r *procfs.Reader, pid int, pagesize uint64) (ProcInfo, error) {
	stat, err := r.ReadStat(pid)
	if err != nil {
		return ProcInfo{}, err
	}
	statm, err := r.ReadStatm(pid)
	if err != nil {
		return ProcInfo{}, err
	}
	status, err := r.ReadStatus(pid)
	if err != nil {
		return ProcInfo{}, err
	}

	info := ProcInfo{
		CPU: CPUInfo{
			User: ticksToDuration(stat.Utime),
			Sys:  ticksToDuration(stat.Stime),
		},
		Memory: MemoryInfo{
			Size:            statm.Size * pagesize,
			Resident:        statm.Resident * pagesize,
			Shared:          statm.Shared * pagesize,
			Text:            statm.Text * pagesize,
			Data:            statm.Data * pagesize,
			MajorPageFaults: stat.Majflt,
			MinorPageFaults: stat.Minflt,
		},
		Threads: ThreadInfo{
			Num:                        uint64(max(stat.NumThreads, 0)),
			VoluntaryContextSwitches:   status.VoluntaryCtxtSwitches,
			InvoluntaryContextSwitches: status.NonvoluntaryCtxtSwitches,
		},
	}

	// Best-effort sources: a missing or unreadable file leaves zero values.
	if cfg, err := r.ReadCPUConfig(pid); err == nil {
		info.CPU.Period, info.CPU.Quota = cfg.Period, cfg.Quota
		info.CPU.Shares, info.CPU.Weight = cfg.Shares, cfg.Weight
	}
	if mi, err := r.ReadMeminfo(); err == nil {
		info.Memory.Available = mi.Available
	}
	if total, err := r.ReadMemoryLimit(pid); err == nil {
		info.Memory.Total = total
	}
	if limits, err := r.ReadLimits(pid); err == nil && limits.OpenFiles.Soft != procfs.Unlimited {
		info.Files.Max = limits.OpenFiles.Soft
	}
	if n, err := r.OpenFileCount(pid); err == nil {
		info.Files.Open = n
	}
	return info, nil
}

// ticksToDuration converts USER_HZ clock ticks to a duration, using the same
// constant 100 Hz as the self-process collector.
func ticksToDuration(ticks uint64) time.Duration {
	return time.Duration(float64(ticks) * float64(time.Second) / clockTicksPerSecond)
}
