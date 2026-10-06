package runtimemetrics

import (
	"errors"
	"os"
	"runtime"

	"github.com/convoy-road-trips-app/stats/runtimemetrics/procfs"
)

// emitCPU emits the CPU gauges for the cumulative user and system CPU seconds
// consumed so far (the caller holds c.mu):
//
//   - cpu.usage.seconds{type=user|system} and cpu.usage_total.seconds
//   - cpu.usage.percent: total delta CPU / delta wall / GOMAXPROCS * 100 (unchanged)
//   - cpu.usage_user.percent, cpu.usage_system.percent, cpu.usage_total.percent:
//     the same delta relative to the CPU capacity, which is the cgroup quota
//     (quota / period cores) when one is set and GOMAXPROCS otherwise
//
// The percent series need a baseline, so the first sample emits only the
// seconds. A decreasing total is skipped.
func (c *Collector) emitCPU(user, system float64) {
	c.gauge("cpu.usage.seconds", user, typeAttr("user"))
	c.gauge("cpu.usage.seconds", system, typeAttr("system"))
	c.gauge("cpu.usage_total.seconds", user+system)

	p := c.proc
	now := p.now()
	total := user + system
	if p.havePrev {
		wall := now.Sub(p.prevWall).Seconds()
		if wall > 0 && total >= p.prevCPU && user >= p.prevUser && system >= p.prevSys {
			gomax := float64(runtime.GOMAXPROCS(0))
			c.gauge("cpu.usage.percent", (total-p.prevCPU)/wall/gomax*100)

			capacity := gomax
			if p.cores > 0 {
				capacity = p.cores
			}
			c.gauge("cpu.usage_user.percent", (user-p.prevUser)/wall/capacity*100)
			c.gauge("cpu.usage_system.percent", (system-p.prevSys)/wall/capacity*100)
			c.gauge("cpu.usage_total.percent", (total-p.prevCPU)/wall/capacity*100)
		}
	}
	p.prevUser, p.prevSys, p.prevCPU, p.prevWall, p.havePrev = user, system, total, now, true
}

// collectCgroupCPU emits the cgroup CPU configuration, when there is one:
//
//   - cpu.cgroup.quota.seconds and cpu.cgroup.period.seconds (cgroup v2 cpu.max,
//     v1 cpu.cfs_quota_us / cpu.cfs_period_us); no quota series when unlimited
//   - cpu.cgroup.weight (v2 cpu.weight) or cpu.cgroup.shares (v1 cpu.shares)
//
// It also records the quota in cores for the percent series. A process outside
// any readable cpu cgroup is normal and not an error.
func (c *Collector) collectCgroupCPU() {
	p := c.proc
	p.cores = 0
	if p.src.cpuConfig == nil {
		return
	}
	cfg, err := p.src.cpuConfig()
	if err != nil {
		if !errors.Is(err, procfs.ErrNoCPUCgroup) && !errors.Is(err, os.ErrNotExist) {
			c.processFail("cgroup.cpu", err)
		}
		return
	}

	if cfg.Period > 0 {
		c.gauge("cpu.cgroup.period.seconds", cfg.Period.Seconds())
	}
	if cfg.Quota > 0 {
		c.gauge("cpu.cgroup.quota.seconds", cfg.Quota.Seconds())
		if cfg.Period > 0 {
			p.cores = cfg.Quota.Seconds() / cfg.Period.Seconds()
		}
	}
	if cfg.Weight > 0 {
		c.gauge("cpu.cgroup.weight", float64(cfg.Weight))
	}
	if cfg.Shares > 0 {
		c.gauge("cpu.cgroup.shares", float64(cfg.Shares))
	}
}

// collectStatm emits memory.virtual.bytes, the virtual size of the process
// (/proc/self/statm size, in pages). It is distinct from memory.total.bytes,
// which is the memory capacity of the host or cgroup.
func (c *Collector) collectStatm() {
	b, err := c.proc.src.readFile(procStatmPath)
	if err != nil {
		c.processFail(procStatmPath, err)
		return
	}
	statm, err := procfs.ParseStatm(b)
	if err != nil {
		c.processFail(procStatmPath, err)
		return
	}
	c.gauge("memory.virtual.bytes", float64(statm.Size)*float64(os.Getpagesize()))
}

// emitResidentPercent emits memory.usage.percent{type=resident}: the resident
// set size as a percentage of memory.total.bytes (host memory, capped by the
// cgroup limit). Nothing is emitted unless both were read in this collection.
func (c *Collector) emitResidentPercent() {
	p := c.proc
	if p.haveResident && p.haveTotal && p.total > 0 {
		c.gauge("memory.usage.percent", float64(p.resident)/float64(p.total)*100, typeAttr("resident"))
	}
}
