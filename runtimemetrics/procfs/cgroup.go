package procfs

import (
	"strconv"
	"strings"
)

// CGroup is one controller membership line of /proc/<pid>/cgroup.
type CGroup struct {
	// ID is the hierarchy ID; 0 for the cgroup v2 unified hierarchy.
	ID int
	// Name is a single controller (for example "cpu" or "memory"), the
	// "name=systemd" label without the prefix, or empty for cgroup v2.
	Name string
	// Path is the cgroup path relative to the cgroup mount ("/" at the root).
	Path string
}

// CGroups is the parsed content of /proc/<pid>/cgroup. A v1 line that lists
// several controllers ("cpu,cpuacct") yields one CGroup per controller.
type CGroups []CGroup

// Lookup returns the entry of the named controller. A name such as
// "cpu,cpuacct" matches when any of its comma separated parts matches.
func (cs CGroups) Lookup(name string) (CGroup, bool) {
	for _, want := range strings.Split(name, ",") {
		for _, c := range cs {
			if c.Name != "" && c.Name == want {
				return c, true
			}
		}
	}
	return CGroup{}, false
}

// Unified returns the cgroup v2 entry (hierarchy 0, no controller list).
func (cs CGroups) Unified() (CGroup, bool) {
	for _, c := range cs {
		if c.ID == 0 && c.Name == "" {
			return c, true
		}
	}
	return CGroup{}, false
}

// ParseCGroups parses /proc/<pid>/cgroup, whose lines have the form
// "hierarchy-ID:controller-list:cgroup-path".
func ParseCGroups(b []byte) (CGroups, error) {
	var out CGroups
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			return nil, malformed("cgroup", "line %q", line)
		}
		id, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, malformed("cgroup", "hierarchy id: %v", err)
		}
		path := strings.TrimSpace(parts[2])
		if parts[1] == "" {
			out = append(out, CGroup{ID: id, Path: path})
			continue
		}
		for _, name := range strings.Split(parts[1], ",") {
			out = append(out, CGroup{ID: id, Name: strings.TrimPrefix(name, "name="), Path: path})
		}
	}
	return out, nil
}

// ReadCGroups reads and parses /proc/<pid>/cgroup.
func (r *Reader) ReadCGroups(pid int) (CGroups, error) {
	b, err := r.readProc(pid, "cgroup")
	if err != nil {
		return nil, err
	}
	return ParseCGroups(b)
}

// ReadCGroups reads /proc/<pid>/cgroup with the Default reader.
func ReadCGroups(pid int) (CGroups, error) { return Default.ReadCGroups(pid) }
