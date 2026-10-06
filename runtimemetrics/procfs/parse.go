package procfs

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrMalformed is wrapped by every parse error, so callers can test for it
// with errors.Is.
var ErrMalformed = errors.New("procfs: malformed input")

func malformed(file, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrMalformed, file, fmt.Sprintf(format, args...))
}

// KeyValues parses "Key: value [kB]" files such as /proc/<pid>/status and
// /proc/meminfo. Values with a kB unit are returned in bytes. Lines whose value
// is not an unsigned integer (such as "Name: foo") are skipped.
func KeyValues(b []byte) map[string]uint64 {
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

// parseUints parses the whitespace separated fields of s, starting at index
// from, into dst, and reports the first field that is not an unsigned integer.
func parseUints(file string, fields []string, dst ...*uint64) error {
	if len(fields) < len(dst) {
		return malformed(file, "%d fields, need %d", len(fields), len(dst))
	}
	for i, p := range dst {
		v, err := strconv.ParseUint(fields[i], 10, 64)
		if err != nil {
			return malformed(file, "field %d: %v", i+1, err)
		}
		*p = v
	}
	return nil
}
