package runtimemetrics

import (
	"errors"
	"fmt"
	"runtime"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats/models"
	"github.com/convoy-road-trips-app/stats/runtimemetrics/procfs"
)

// Linux sources read by the process metrics.
const (
	procStatPath    = "/proc/self/stat"
	procStatusPath  = "/proc/self/status"
	procLimitsPath  = "/proc/self/limits"
	procMeminfoPath = "/proc/meminfo"
	procFDDir       = "/proc/self/fd"
	cgroupMemoryMax = "/sys/fs/cgroup/memory.max"
)

// clockTicksPerSecond is the assumed USER_HZ. Linux exposes /proc/self/stat
// times in USER_HZ ticks, which is 100 on every mainstream architecture.
// Reading it through sysconf(_SC_CLK_TCK) would need cgo, so it is a constant.
const clockTicksPerSecond = 100

var errNoOpenFilesLimit = errors.New("no open files limit in /proc/self/limits")

// processSource abstracts the filesystem reads behind process metrics so
// tests can inject fixtures.
type processSource struct {
	// readFile returns the full contents of the file at path.
	readFile func(path string) ([]byte, error)
	// countDir returns the number of entries in the directory at path.
	countDir func(path string) (int, error)
}

// processState holds the state process metrics keep between collections.
type processState struct {
	// src is the procfs source read by collectProcfs. It is nil for platform
	// implementations that set collect themselves (Darwin).
	src *processSource
	// collect emits one round of process metrics. The caller holds Collector.mu.
	collect func(c *Collector)
	now     func() time.Time

	// prevCPU is the previous total CPU time in seconds, taken at prevWall.
	// havePrev is false until the first successful sample.
	prevCPU  float64
	prevWall time.Time
	havePrev bool

	// reported records the sources already passed to OnError.
	reported map[string]bool
}

// newProcessState returns the state for the procfs (Linux) collection backed by src.
func newProcessState(src *processSource) *processState {
	return &processState{
		src:      src,
		collect:  (*Collector).collectProcfs,
		now:      time.Now,
		reported: make(map[string]bool),
	}
}

func typeAttr(v string) attribute.KeyValue { return attribute.String("type", v) }

// processFail reports err for the named source at most once. The source is skipped
// for this collection either way and retried on the next.
func (c *Collector) processFail(path string, err error) {
	p := c.proc
	if p.reported[path] {
		return
	}
	p.reported[path] = true
	if c.cfg.OnError != nil {
		c.cfg.OnError("process", fmt.Errorf("%s: %w", path, err))
	}
}

// collectProcess emits process-level metrics. The caller holds c.mu.
func (c *Collector) collectProcess() {
	if !c.cfg.ProcessMetrics || c.proc == nil {
		return
	}
	c.proc.collect(c)
}

// collectProcfs emits the process metrics read from /proc and /sys.
func (c *Collector) collectProcfs() {
	c.collectProcStat()
	c.collectProcStatus()
	c.collectLimits()
	c.collectFDCount()
	c.collectSystemMemory()
}

func (c *Collector) gauge(name string, value float64, attrs ...attribute.KeyValue) {
	c.record(c.prefix()+name, models.MetricTypeGauge, value, attrs...)
}

func (c *Collector) collectProcStat() {
	p := c.proc
	b, err := p.src.readFile(procStatPath)
	if err != nil {
		c.processFail(procStatPath, err)
		return
	}
	st, err := procfs.ParseStat(b)
	if err != nil {
		c.processFail(procStatPath, err)
		return
	}

	user := float64(st.Utime) / clockTicksPerSecond
	system := float64(st.Stime) / clockTicksPerSecond
	c.gauge("cpu.usage.seconds", user, typeAttr("user"))
	c.gauge("cpu.usage.seconds", system, typeAttr("system"))
	c.gauge("memory.pagefault.count", float64(st.Majflt), typeAttr("major"))
	c.gauge("memory.pagefault.count", float64(st.Minflt), typeAttr("minor"))
	c.gauge("threads.count", float64(st.NumThreads))

	c.emitCPUPercent(user + system)
}

// emitCPUPercent emits cpu.usage.percent from the total CPU seconds consumed
// so far: delta CPU / delta wall / GOMAXPROCS * 100. The first sample only
// records the baseline and emits nothing; a decreasing total is skipped.
func (c *Collector) emitCPUPercent(total float64) {
	p := c.proc
	now := p.now()
	if p.havePrev {
		wall := now.Sub(p.prevWall).Seconds()
		if wall > 0 && total >= p.prevCPU {
			pct := (total - p.prevCPU) / wall / float64(runtime.GOMAXPROCS(0)) * 100
			c.gauge("cpu.usage.percent", pct)
		}
	}
	p.prevCPU, p.prevWall, p.havePrev = total, now, true
}

func (c *Collector) collectProcStatus() {
	b, err := c.proc.src.readFile(procStatusPath)
	if err != nil {
		c.processFail(procStatusPath, err)
		return
	}
	kv := procfs.KeyValues(b)

	if v, ok := kv["VmRSS"]; ok {
		c.gauge("memory.usage.bytes", float64(v), typeAttr("resident"))
	}
	file, hasFile := kv["RssFile"]
	shmem, hasShmem := kv["RssShmem"]
	if hasFile || hasShmem {
		c.gauge("memory.usage.bytes", float64(file+shmem), typeAttr("shared"))
	}
	if v, ok := kv["VmExe"]; ok {
		c.gauge("memory.usage.bytes", float64(v), typeAttr("text"))
	}
	if v, ok := kv["VmData"]; ok {
		c.gauge("memory.usage.bytes", float64(v), typeAttr("data"))
	}
	if v, ok := kv["voluntary_ctxt_switches"]; ok {
		c.gauge("threads.switch.count", float64(v), typeAttr("voluntary"))
	}
	if v, ok := kv["nonvoluntary_ctxt_switches"]; ok {
		c.gauge("threads.switch.count", float64(v), typeAttr("involuntary"))
	}
}

func (c *Collector) collectLimits() {
	b, err := c.proc.src.readFile(procLimitsPath)
	if err != nil {
		c.processFail(procLimitsPath, err)
		return
	}
	limits, err := procfs.ParseLimits(b)
	if err != nil {
		c.processFail(procLimitsPath, err)
		return
	}
	switch open := limits.OpenFiles; {
	case open.Name == "":
		c.processFail(procLimitsPath, errNoOpenFilesLimit)
	case open.Soft != procfs.Unlimited:
		c.gauge("files.open.max", float64(open.Soft))
	}
}

func (c *Collector) collectFDCount() {
	n, err := c.proc.src.countDir(procFDDir)
	if err != nil {
		c.processFail(procFDDir, err)
		return
	}
	c.gauge("files.open.count", float64(n))
}

func (c *Collector) collectSystemMemory() {
	b, err := c.proc.src.readFile(procMeminfoPath)
	if err != nil {
		c.processFail(procMeminfoPath, err)
		return
	}
	kv := procfs.KeyValues(b)

	if v, ok := kv["MemAvailable"]; ok {
		c.gauge("memory.available.bytes", float64(v))
	}
	total, ok := kv["MemTotal"]
	if !ok {
		return
	}
	// cgroup v2 may cap memory below the host total. A missing file (cgroup
	// v1, no cgroup mount) is normal and not an error.
	if mb, err := c.proc.src.readFile(cgroupMemoryMax); err == nil {
		if limit, ok, err := procfs.ParseMemoryLimit(mb); err == nil && ok && limit < total {
			total = limit
		}
	}
	c.gauge("memory.total.bytes", float64(total))
}
