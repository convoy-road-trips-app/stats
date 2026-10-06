package procfs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// CPUConfig is the CPU bandwidth and weight configuration of a cgroup. Zero
// values mean "not known or not limited".
type CPUConfig struct {
	// Quota is the CPU time allowed per Period; zero when unlimited or unknown.
	Quota time.Duration
	// Period is the bandwidth enforcement period.
	Period time.Duration
	// Shares is the cgroup v1 relative weight (cpu.shares, default 1024).
	Shares int64
	// Weight is the cgroup v2 relative weight (cpu.weight, 1-10000, default 100).
	Weight int64
}

// ParseCPUMax parses a cgroup v2 cpu.max file: "<quota|max> [period]". A "max"
// quota yields Quota 0 (unlimited). Period defaults to 100ms when absent.
func ParseCPUMax(b []byte) (CPUConfig, error) {
	f := strings.Fields(string(b))
	if len(f) == 0 || len(f) > 2 {
		return CPUConfig{}, malformed("cpu.max", "%d fields", len(f))
	}
	cfg := CPUConfig{Period: 100 * time.Millisecond}
	if len(f) == 2 {
		p, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil || p <= 0 {
			return CPUConfig{}, malformed("cpu.max", "period %q", f[1])
		}
		cfg.Period = time.Duration(p) * time.Microsecond
	}
	if f[0] != "max" {
		q, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil || q <= 0 {
			return CPUConfig{}, malformed("cpu.max", "quota %q", f[0])
		}
		cfg.Quota = time.Duration(q) * time.Microsecond
	}
	return cfg, nil
}

// parseInt64File parses a cgroup file holding one integer.
func parseInt64File(name string, b []byte) (int64, error) {
	v, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return 0, malformed(name, "%v", err)
	}
	return v, nil
}

// ErrNoCPUCgroup is returned by ReadCPUConfig when the process has neither a
// cgroup v2 nor a v1 "cpu" cgroup that can be read.
var ErrNoCPUCgroup = errors.New("procfs: no readable cpu cgroup")

// ReadCPUConfig returns the CPU configuration of the cgroup that pid belongs
// to, from cgroup v2 cpu.max and cpu.weight, or cgroup v1 cpu.cfs_quota_us,
// cpu.cfs_period_us and cpu.shares. Files that do not exist are skipped.
//
// A container often sees its own cgroup as the root of the mount, so when
// the path in /proc/<pid>/cgroup does not exist and pid is the calling process,
// the root of the cgroup mount is read instead.
func (r *Reader) ReadCPUConfig(pid int) (CPUConfig, error) {
	groups, err := r.ReadCGroups(pid)
	if err != nil {
		return CPUConfig{}, err
	}
	if g, ok := groups.Unified(); ok {
		if dir, ok := r.cgroupDir(pid, g.Path, ""); ok {
			return readCPUv2(dir)
		}
	}
	if g, ok := groups.Lookup("cpu"); ok {
		if dir, ok := r.cgroupDir(pid, g.Path, "cpu"); ok {
			return readCPUv1(dir)
		}
	}
	return CPUConfig{}, ErrNoCPUCgroup
}

// ReadCPUConfig reads the cgroup CPU configuration of pid with the Default reader.
func ReadCPUConfig(pid int) (CPUConfig, error) { return Default.ReadCPUConfig(pid) }

// cgroupDir resolves the directory of a cgroup. controller is the v1
// sub-directory ("cpu", "memory") or empty for v2.
func (r *Reader) cgroupDir(pid int, cgPath, controller string) (string, bool) {
	base := filepath.Join(r.cgroupRoot(), controller)
	dir := filepath.Join(base, filepath.Clean("/"+cgPath))
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		return dir, true
	}
	if pid == r.self() {
		if st, err := os.Stat(base); err == nil && st.IsDir() {
			return base, true
		}
	}
	return "", false
}

// cgroupFile returns the path of a file inside a cgroup directory.
func cgroupFile(dir, name string) string { return filepath.Join(dir, name) }

func readCPUv2(dir string) (CPUConfig, error) {
	var cfg CPUConfig
	b, err := os.ReadFile(cgroupFile(dir, "cpu.max"))
	switch {
	case err == nil:
		if cfg, err = ParseCPUMax(b); err != nil {
			return CPUConfig{}, err
		}
	case !errors.Is(err, fs.ErrNotExist):
		return CPUConfig{}, err
	}
	if b, err := os.ReadFile(cgroupFile(dir, "cpu.weight")); err == nil {
		if cfg.Weight, err = parseInt64File("cpu.weight", b); err != nil {
			return CPUConfig{}, err
		}
	}
	return cfg, nil
}

func readCPUv1(dir string) (CPUConfig, error) {
	var cfg CPUConfig
	read := func(name string) (int64, bool, error) {
		b, err := os.ReadFile(cgroupFile(dir, name))
		if errors.Is(err, fs.ErrNotExist) {
			return 0, false, nil
		}
		if err != nil {
			return 0, false, err
		}
		v, err := parseInt64File(name, b)
		return v, err == nil, err
	}

	if v, ok, err := read("cpu.cfs_period_us"); err != nil {
		return CPUConfig{}, fmt.Errorf("cpu.cfs_period_us: %w", err)
	} else if ok && v > 0 {
		cfg.Period = time.Duration(v) * time.Microsecond
	}
	// A quota of -1 means unlimited and stays zero.
	if v, ok, err := read("cpu.cfs_quota_us"); err != nil {
		return CPUConfig{}, fmt.Errorf("cpu.cfs_quota_us: %w", err)
	} else if ok && v > 0 {
		cfg.Quota = time.Duration(v) * time.Microsecond
	}
	if v, ok, err := read("cpu.shares"); err != nil {
		return CPUConfig{}, fmt.Errorf("cpu.shares: %w", err)
	} else if ok {
		cfg.Shares = v
	}
	return cfg, nil
}
