package procfs

import (
	"strconv"
	"strings"
)

// Sched holds scheduling statistics from /proc/<pid>/sched. Fields the kernel
// does not expose (the file is optional and varies by kernel config) stay zero.
type Sched struct {
	NRSwitches            uint64 // nr_switches
	NRVoluntarySwitches   uint64 // nr_voluntary_switches
	NRInvoluntarySwitches uint64 // nr_involuntary_switches
	SEAvgLoadSum          uint64 // se.avg.load_sum
	SEAvgUtilSum          uint64 // se.avg.util_sum
	SEAvgLoadAvg          uint64 // se.avg.load_avg
	SEAvgUtilAvg          uint64 // se.avg.util_avg
}

// ParseSched parses /proc/<pid>/sched. The two header lines are ignored and
// properties with a non-integer value (for example fractional times) are
// skipped. Repeated properties, which may appear once per thread, are summed.
func ParseSched(b []byte) (Sched, error) {
	var s Sched
	fields := map[string]*uint64{
		"nr_switches":             &s.NRSwitches,
		"nr_voluntary_switches":   &s.NRVoluntarySwitches,
		"nr_involuntary_switches": &s.NRInvoluntarySwitches,
		"se.avg.load_sum":         &s.SEAvgLoadSum,
		"se.avg.util_sum":         &s.SEAvgUtilSum,
		"se.avg.load_avg":         &s.SEAvgLoadAvg,
		"se.avg.util_avg":         &s.SEAvgUtilAvg,
	}

	lines := strings.Split(string(b), "\n")
	if len(lines) < 2 {
		return Sched{}, malformed("sched", "missing header")
	}
	for _, line := range lines[2:] {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		dst := fields[strings.TrimSpace(key)]
		if dst == nil {
			continue
		}
		if v, err := strconv.ParseUint(strings.TrimSpace(val), 10, 64); err == nil {
			*dst += v
		}
	}
	return s, nil
}

// ReadSched reads and parses /proc/<pid>/sched.
func (r *Reader) ReadSched(pid int) (Sched, error) {
	b, err := r.readProc(pid, "sched")
	if err != nil {
		return Sched{}, err
	}
	return ParseSched(b)
}

// ReadSched reads /proc/<pid>/sched with the Default reader.
func ReadSched(pid int) (Sched, error) { return Default.ReadSched(pid) }
