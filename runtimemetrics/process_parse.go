package runtimemetrics

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// The parsers in this file are pure functions over file contents, so they
// build and are testable on every platform.

// procStat holds the fields of /proc/self/stat the collector uses.
type procStat struct {
	utime      uint64 // user mode time, clock ticks
	stime      uint64 // kernel mode time, clock ticks
	minflt     uint64 // minor page faults
	majflt     uint64 // major page faults
	numThreads uint64
}

// Field positions in /proc/[pid]/stat (proc(5)) counted from the first field
// after the closing parenthesis of comm, which is field 3 ("state").
const (
	statIdxMinflt     = 10 - 3
	statIdxMajflt     = 12 - 3
	statIdxUtime      = 14 - 3
	statIdxStime      = 15 - 3
	statIdxNumThreads = 20 - 3
)

var errProcStatMalformed = errors.New("malformed /proc/self/stat")

// parseProcStat parses /proc/[pid]/stat. The comm field is wrapped in
// parentheses and may itself contain spaces and parentheses, so fields are
// split only after the LAST ')'.
func parseProcStat(b []byte) (procStat, error) {
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return procStat{}, fmt.Errorf("%w: no closing parenthesis", errProcStatMalformed)
	}
	fields := strings.Fields(string(b[i+1:]))
	if len(fields) <= statIdxNumThreads {
		return procStat{}, fmt.Errorf("%w: %d fields after comm", errProcStatMalformed, len(fields))
	}

	var out procStat
	for _, f := range []struct {
		dst *uint64
		idx int
	}{
		{&out.minflt, statIdxMinflt},
		{&out.majflt, statIdxMajflt},
		{&out.utime, statIdxUtime},
		{&out.stime, statIdxStime},
		{&out.numThreads, statIdxNumThreads},
	} {
		v, err := strconv.ParseUint(fields[f.idx], 10, 64)
		if err != nil {
			return procStat{}, fmt.Errorf("%w: field %d: %w", errProcStatMalformed, f.idx+3, err)
		}
		*f.dst = v
	}
	return out, nil
}

// parseKeyValues parses "Key: value [kB]" files such as /proc/[pid]/status
// and /proc/meminfo. Values with a kB unit are returned in bytes. Lines with
// non-numeric values or other shapes are skipped.
func parseKeyValues(b []byte) map[string]uint64 {
	out := make(map[string]uint64)
	for _, line := range strings.Split(string(b), "\n") {
		key, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 || len(fields) > 2 {
			continue
		}
		v, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		switch {
		case len(fields) == 1:
		case fields[1] == "kB":
			v *= 1024
		default:
			continue
		}
		out[strings.TrimSpace(key)] = v
	}
	return out
}

const openFilesLimitLabel = "Max open files"

var errLimitNotFound = errors.New("no open files limit in /proc/self/limits")

// parseOpenFilesLimit returns the soft "Max open files" limit from
// /proc/[pid]/limits. unlimited is true when the limit is "unlimited".
func parseOpenFilesLimit(b []byte) (limit uint64, unlimited bool, err error) {
	for _, line := range strings.Split(string(b), "\n") {
		rest, ok := strings.CutPrefix(line, openFilesLimitLabel)
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			break
		}
		if fields[0] == "unlimited" {
			return 0, true, nil
		}
		v, perr := strconv.ParseUint(fields[0], 10, 64)
		if perr != nil {
			return 0, false, fmt.Errorf("open files limit: %w", perr)
		}
		return v, false, nil
	}
	return 0, false, errLimitNotFound
}

// parseCgroupMemoryMax parses a cgroup v2 memory.max file. ok is false when
// the value is "max" (no limit) or not a number.
func parseCgroupMemoryMax(b []byte) (limit uint64, ok bool) {
	v, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
