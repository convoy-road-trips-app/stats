package procfs

import (
	"os"
	"path/filepath"
	"strconv"
)

// Default roots of the Linux procfs and cgroup file systems.
const (
	DefaultProcRoot   = "/proc"
	DefaultCgroupRoot = "/sys/fs/cgroup"
)

// Reader reads procfs and cgroup files below configurable roots. The zero value
// reads the real /proc and /sys/fs/cgroup.
type Reader struct {
	// ProcRoot is the procfs mount point; empty means DefaultProcRoot.
	ProcRoot string
	// CgroupRoot is the cgroup mount point; empty means DefaultCgroupRoot.
	CgroupRoot string

	// selfPID overrides os.Getpid for tests; zero means os.Getpid().
	selfPID int
}

// Default is the Reader behind the package-level Read functions.
var Default = &Reader{}

func (r *Reader) procRoot() string {
	if r.ProcRoot == "" {
		return DefaultProcRoot
	}
	return r.ProcRoot
}

func (r *Reader) cgroupRoot() string {
	if r.CgroupRoot == "" {
		return DefaultCgroupRoot
	}
	return r.CgroupRoot
}

func (r *Reader) self() int {
	if r.selfPID != 0 {
		return r.selfPID
	}
	return os.Getpid()
}

// procPath returns the path of /proc/<pid>/<name>.
func (r *Reader) procPath(pid int, name string) string {
	return filepath.Join(r.procRoot(), strconv.Itoa(pid), name)
}

func (r *Reader) readProc(pid int, name string) ([]byte, error) {
	return os.ReadFile(r.procPath(pid, name))
}
