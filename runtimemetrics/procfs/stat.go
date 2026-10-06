package procfs

import (
	"bytes"
	"strconv"
	"strings"
)

// Stat holds the leading fields of /proc/<pid>/stat (proc(5)). Times are in
// clock ticks (USER_HZ, 100 on mainstream Linux); sizes are as documented per
// field.
type Stat struct {
	PID        int    // (1) pid
	Comm       string // (2) comm, without the surrounding parentheses
	State      byte   // (3) state, e.g. 'R', 'S', 'Z'
	PPID       int    // (4)
	PGRP       int    // (5)
	Session    int    // (6)
	TTYNr      int    // (7)
	TPGID      int    // (8)
	Flags      uint64 // (9)
	Minflt     uint64 // (10) minor page faults
	Cminflt    uint64 // (11)
	Majflt     uint64 // (12) major page faults
	Cmajflt    uint64 // (13)
	Utime      uint64 // (14) user mode time, ticks
	Stime      uint64 // (15) kernel mode time, ticks
	Cutime     int64  // (16)
	Cstime     int64  // (17)
	Priority   int64  // (18)
	Nice       int64  // (19)
	NumThreads int64  // (20)
	Starttime  uint64 // (22) start time after boot, ticks
	Vsize      uint64 // (23) virtual memory size, bytes
	Rss        int64  // (24) resident set size, pages
}

// statFieldsAfterComm is the number of fields from (3) state through (24) rss.
const statFieldsAfterComm = 24 - 2

// ParseStat parses /proc/<pid>/stat. comm may contain spaces and parentheses,
// so the fields that follow it are split after the LAST ')'.
func ParseStat(b []byte) (Stat, error) {
	const file = "stat"
	open := bytes.IndexByte(b, '(')
	end := bytes.LastIndexByte(b, ')')
	if open < 0 || end < open {
		return Stat{}, malformed(file, "no parenthesised comm")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b[:open])))
	if err != nil {
		return Stat{}, malformed(file, "pid: %v", err)
	}
	f := strings.Fields(string(b[end+1:]))
	if len(f) < statFieldsAfterComm {
		return Stat{}, malformed(file, "%d fields after comm, need %d", len(f), statFieldsAfterComm)
	}
	if len(f[0]) != 1 {
		return Stat{}, malformed(file, "state %q", f[0])
	}

	s := Stat{PID: pid, Comm: string(b[open+1 : end]), State: f[0][0]}
	// f[i] is field number i+3.
	ints := []struct {
		dst *int
		idx int
	}{{&s.PPID, 4}, {&s.PGRP, 5}, {&s.Session, 6}, {&s.TTYNr, 7}, {&s.TPGID, 8}}
	for _, p := range ints {
		if *p.dst, err = strconv.Atoi(f[p.idx-3]); err != nil {
			return Stat{}, malformed(file, "field %d: %v", p.idx, err)
		}
	}
	signed := []struct {
		dst *int64
		idx int
	}{{&s.Cutime, 16}, {&s.Cstime, 17}, {&s.Priority, 18}, {&s.Nice, 19}, {&s.NumThreads, 20}, {&s.Rss, 24}}
	for _, p := range signed {
		if *p.dst, err = strconv.ParseInt(f[p.idx-3], 10, 64); err != nil {
			return Stat{}, malformed(file, "field %d: %v", p.idx, err)
		}
	}
	unsigned := []struct {
		dst *uint64
		idx int
	}{{&s.Flags, 9}, {&s.Minflt, 10}, {&s.Cminflt, 11}, {&s.Majflt, 12}, {&s.Cmajflt, 13},
		{&s.Utime, 14}, {&s.Stime, 15}, {&s.Starttime, 22}, {&s.Vsize, 23}}
	for _, p := range unsigned {
		if *p.dst, err = strconv.ParseUint(f[p.idx-3], 10, 64); err != nil {
			return Stat{}, malformed(file, "field %d: %v", p.idx, err)
		}
	}
	return s, nil
}

// ReadStat reads and parses /proc/<pid>/stat.
func (r *Reader) ReadStat(pid int) (Stat, error) {
	b, err := r.readProc(pid, "stat")
	if err != nil {
		return Stat{}, err
	}
	return ParseStat(b)
}

// ReadStat reads /proc/<pid>/stat with the Default reader.
func ReadStat(pid int) (Stat, error) { return Default.ReadStat(pid) }
