package procfs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Meminfo holds the fields of /proc/meminfo that process metrics use, in bytes.
type Meminfo struct {
	Total     uint64 // MemTotal
	Free      uint64 // MemFree
	Available uint64 // MemAvailable; zero on kernels older than 3.14
	Buffers   uint64
	Cached    uint64
	SwapTotal uint64
	SwapFree  uint64
}

// ParseMeminfo parses /proc/meminfo. MemTotal is required.
func ParseMeminfo(b []byte) (Meminfo, error) {
	kv := KeyValues(b)
	total, ok := kv["MemTotal"]
	if !ok {
		return Meminfo{}, malformed("meminfo", "no MemTotal")
	}
	return Meminfo{
		Total: total, Free: kv["MemFree"], Available: kv["MemAvailable"],
		Buffers: kv["Buffers"], Cached: kv["Cached"],
		SwapTotal: kv["SwapTotal"], SwapFree: kv["SwapFree"],
	}, nil
}

// ReadMeminfo reads and parses /proc/meminfo.
func (r *Reader) ReadMeminfo() (Meminfo, error) {
	b, err := os.ReadFile(filepath.Join(r.procRoot(), "meminfo"))
	if err != nil {
		return Meminfo{}, err
	}
	return ParseMeminfo(b)
}

// ReadMeminfo reads /proc/meminfo with the Default reader.
func ReadMeminfo() (Meminfo, error) { return Default.ReadMeminfo() }

// Status holds the fields of /proc/<pid>/status that process metrics use.
// Sizes are in bytes (the kernel prints kB). A field the kernel does not print,
// such as RssFile before Linux 4.5, is zero.
type Status struct {
	VmSize   uint64
	VmRSS    uint64
	RssAnon  uint64
	RssFile  uint64
	RssShmem uint64
	VmData   uint64
	VmStk    uint64
	VmExe    uint64
	VmLib    uint64

	Threads                  uint64
	VoluntaryCtxtSwitches    uint64
	NonvoluntaryCtxtSwitches uint64
}

// ParseStatus parses /proc/<pid>/status.
func ParseStatus(b []byte) (Status, error) {
	kv := KeyValues(b)
	if len(kv) == 0 {
		return Status{}, malformed("status", "no numeric fields")
	}
	return Status{
		VmSize: kv["VmSize"], VmRSS: kv["VmRSS"], RssAnon: kv["RssAnon"],
		RssFile: kv["RssFile"], RssShmem: kv["RssShmem"], VmData: kv["VmData"],
		VmStk: kv["VmStk"], VmExe: kv["VmExe"], VmLib: kv["VmLib"],
		Threads:                  kv["Threads"],
		VoluntaryCtxtSwitches:    kv["voluntary_ctxt_switches"],
		NonvoluntaryCtxtSwitches: kv["nonvoluntary_ctxt_switches"],
	}, nil
}

// ReadStatus reads and parses /proc/<pid>/status.
func (r *Reader) ReadStatus(pid int) (Status, error) {
	b, err := r.readProc(pid, "status")
	if err != nil {
		return Status{}, err
	}
	return ParseStatus(b)
}

// ReadStatus reads /proc/<pid>/status with the Default reader.
func ReadStatus(pid int) (Status, error) { return Default.ReadStatus(pid) }

// ParseMemoryLimit parses a cgroup memory limit file (v2 memory.max or v1
// memory.limit_in_bytes). ok is false for "max" and for the v1 "no limit"
// sentinel (a value close to 2^63), which are both reported as unlimited.
func ParseMemoryLimit(b []byte) (limit uint64, ok bool, err error) {
	s := strings.TrimSpace(string(b))
	if s == "max" {
		return 0, false, nil
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, false, malformed("memory limit", "%v", err)
	}
	if v >= unlimitedV1Memory {
		return 0, false, nil
	}
	return v, true, nil
}

// unlimitedV1Memory is the smallest value cgroup v1 prints for "no limit"
// (page-aligned 2^63-1 is 9223372036854771712).
const unlimitedV1Memory = 9223372036854771712

// ReadMemoryLimit returns the memory limit in bytes that applies to pid: the
// cgroup v2 memory.max or v1 memory.limit_in_bytes of its cgroup when numeric,
// otherwise MemTotal from /proc/meminfo. It uses the same cgroup path
// resolution as ReadCPUConfig.
func (r *Reader) ReadMemoryLimit(pid int) (uint64, error) {
	if groups, err := r.ReadCGroups(pid); err == nil {
		if limit, ok := r.cgroupMemoryLimit(pid, groups); ok {
			return limit, nil
		}
	}
	mi, err := r.ReadMeminfo()
	if err != nil {
		return 0, err
	}
	return mi.Total, nil
}

// ReadMemoryLimit returns the memory limit of pid with the Default reader.
func ReadMemoryLimit(pid int) (uint64, error) { return Default.ReadMemoryLimit(pid) }

func (r *Reader) cgroupMemoryLimit(pid int, groups CGroups) (uint64, bool) {
	read := func(dir, file string) (uint64, bool) {
		b, err := os.ReadFile(cgroupFile(dir, file))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return 0, false
		}
		limit, ok, perr := ParseMemoryLimit(b)
		return limit, err == nil && perr == nil && ok
	}
	if g, ok := groups.Unified(); ok {
		if dir, ok := r.cgroupDir(pid, g.Path, ""); ok {
			if limit, ok := read(dir, "memory.max"); ok {
				return limit, true
			}
		}
	}
	if g, ok := groups.Lookup("memory"); ok {
		if dir, ok := r.cgroupDir(pid, g.Path, "memory"); ok {
			return read(dir, "memory.limit_in_bytes")
		}
	}
	return 0, false
}
