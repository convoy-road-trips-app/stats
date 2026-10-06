package runtimemetrics

import (
	"bytes"
	"strings"
)

// parseCPUInfoPhysical counts the physical cores listed in /proc/cpuinfo as the
// number of distinct (physical id, core id) pairs. ok is false when the file
// carries no topology fields (common on ARM), in which case the physical count
// is unknown rather than guessed.
func parseCPUInfoPhysical(b []byte) (n int, ok bool) {
	type core struct{ pkg, id string }
	seen := make(map[core]struct{})

	var cur core
	var havePkg, haveID bool
	flush := func() {
		if havePkg || haveID {
			seen[cur] = struct{}{}
		}
		cur, havePkg, haveID = core{}, false, false
	}

	for _, line := range bytes.Split(b, []byte("\n")) {
		key, val, found := strings.Cut(string(line), ":")
		if !found {
			if len(bytes.TrimSpace(line)) == 0 {
				flush()
			}
			continue
		}
		switch strings.TrimSpace(key) {
		case "physical id":
			cur.pkg, havePkg = strings.TrimSpace(val), true
		case "core id":
			cur.id, haveID = strings.TrimSpace(val), true
		}
	}
	flush()

	if len(seen) == 0 {
		return 0, false
	}
	return len(seen), true
}
