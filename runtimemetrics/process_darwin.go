//go:build darwin

package runtimemetrics

import "syscall"

// processSourceRusage is the name under which a failing getrusage(2) call is
// reported through Config.OnError.
const processSourceRusage = "getrusage"

// rusageReader fills ru with the resource usage of the calling process. It is a
// seam so tests can inject values and failures.
type rusageReader func(ru *syscall.Rusage) error

// readSelfRusage is the real reader: getrusage(RUSAGE_SELF) via the standard
// library, with no cgo.
func readSelfRusage(ru *syscall.Rusage) error {
	return syscall.Getrusage(syscall.RUSAGE_SELF, ru)
}

// newPlatformProcessState returns the Darwin process metrics state backed by
// getrusage(2).
func newPlatformProcessState() *processState {
	return newDarwinProcessState(readSelfRusage)
}

// newDarwinProcessState returns process metrics state that samples read.
func newDarwinProcessState(read rusageReader) *processState {
	p := newProcessState(nil)
	p.collect = func(c *Collector) { c.collectRusage(read) }
	return p
}

// collectRusage emits the process metrics getrusage(2) provides:
//
//   - cpu.usage.seconds{type=user|system} from ru_utime/ru_stime
//   - cpu.usage_total.seconds and the percent series, derived (the percents are
//     not emitted on the first sample)
//   - memory.usage.bytes{type=resident} from ru_maxrss
//   - memory.pagefault.count{type=major|minor} from ru_majflt/ru_minflt
//   - threads.switch.count{type=voluntary|involuntary} from ru_nvcsw/ru_nivcsw
//
// ru_maxrss is the peak resident set size, not the current one, and Darwin
// reports it in bytes (Linux reports kilobytes). Metrics rusage cannot supply
// (thread count, open files, system memory, shared/text/data sizes) are not
// emitted. A failing call is reported once through OnError and skipped; it is
// retried on the next collection. The caller holds c.mu.
func (c *Collector) collectRusage(read rusageReader) {
	var ru syscall.Rusage
	if err := read(&ru); err != nil {
		c.processFail(processSourceRusage, err)
		return
	}

	user := timevalSeconds(ru.Utime)
	system := timevalSeconds(ru.Stime)
	c.emitCPU(user, system)
	c.gauge("memory.usage.bytes", float64(ru.Maxrss), typeAttr("resident"))
	c.gauge("memory.pagefault.count", float64(ru.Majflt), typeAttr("major"))
	c.gauge("memory.pagefault.count", float64(ru.Minflt), typeAttr("minor"))
	c.gauge("threads.switch.count", float64(ru.Nvcsw), typeAttr("voluntary"))
	c.gauge("threads.switch.count", float64(ru.Nivcsw), typeAttr("involuntary"))
}

func timevalSeconds(tv syscall.Timeval) float64 {
	return float64(tv.Sec) + float64(tv.Usec)/1e6
}
