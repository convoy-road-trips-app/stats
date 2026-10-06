package procfs

import (
	"regexp"
	"strconv"
	"strings"
)

// Unlimited is the value of a Limit that the kernel reports as "unlimited".
const Unlimited uint64 = 1<<64 - 1

// Limit is one row of /proc/<pid>/limits.
type Limit struct {
	Name string
	Soft uint64 // Unlimited when "unlimited"
	Hard uint64 // Unlimited when "unlimited"
	Unit string // empty when the kernel prints none
}

// Limits holds the resource limits of /proc/<pid>/limits. A row the kernel does
// not print is the zero Limit.
type Limits struct {
	CPUTime          Limit // seconds
	FileSize         Limit // bytes
	DataSize         Limit // bytes
	StackSize        Limit // bytes
	CoreFileSize     Limit // bytes
	ResidentSet      Limit // bytes
	Processes        Limit // processes
	OpenFiles        Limit // files
	LockedMemory     Limit // bytes
	AddressSpace     Limit // bytes
	FileLocks        Limit // locks
	PendingSignals   Limit // signals
	MsgqueueSize     Limit // bytes
	NicePriority     Limit
	RealtimePriority Limit
	RealtimeTimeout  Limit
}

// columnSep splits the fixed-width columns, which are separated by two or more
// spaces (names themselves contain single spaces).
var columnSep = regexp.MustCompile(` {2,}`)

// ParseLimits parses /proc/<pid>/limits.
func ParseLimits(b []byte) (Limits, error) {
	var l Limits
	rows := map[string]*Limit{
		"Max cpu time": &l.CPUTime, "Max file size": &l.FileSize,
		"Max data size": &l.DataSize, "Max stack size": &l.StackSize,
		"Max core file size": &l.CoreFileSize, "Max resident set": &l.ResidentSet,
		"Max processes": &l.Processes, "Max open files": &l.OpenFiles,
		"Max locked memory": &l.LockedMemory, "Max address space": &l.AddressSpace,
		"Max file locks": &l.FileLocks, "Max pending signals": &l.PendingSignals,
		"Max msgqueue size": &l.MsgqueueSize, "Max nice priority": &l.NicePriority,
		"Max realtime priority": &l.RealtimePriority, "Max realtime timeout": &l.RealtimeTimeout,
	}

	lines := strings.Split(string(b), "\n")
	if len(lines) < 2 {
		return Limits{}, malformed("limits", "missing header")
	}
	for _, line := range lines[1:] {
		cols := columnSep.Split(strings.TrimSpace(line), -1)
		if len(cols) < 3 {
			continue
		}
		dst := rows[cols[0]]
		if dst == nil {
			continue
		}
		soft, err := parseLimitValue(cols[1])
		if err != nil {
			return Limits{}, malformed("limits", "%s soft: %v", cols[0], err)
		}
		hard, err := parseLimitValue(cols[2])
		if err != nil {
			return Limits{}, malformed("limits", "%s hard: %v", cols[0], err)
		}
		*dst = Limit{Name: cols[0], Soft: soft, Hard: hard}
		if len(cols) > 3 {
			dst.Unit = cols[3]
		}
	}
	return l, nil
}

func parseLimitValue(s string) (uint64, error) {
	if s == "unlimited" {
		return Unlimited, nil
	}
	return strconv.ParseUint(s, 10, 64)
}

// ReadLimits reads and parses /proc/<pid>/limits.
func (r *Reader) ReadLimits(pid int) (Limits, error) {
	b, err := r.readProc(pid, "limits")
	if err != nil {
		return Limits{}, err
	}
	return ParseLimits(b)
}

// ReadLimits reads /proc/<pid>/limits with the Default reader.
func ReadLimits(pid int) (Limits, error) { return Default.ReadLimits(pid) }
