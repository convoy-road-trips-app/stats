package procfs

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Everything here runs on every platform: parsers work on []byte and the
// Reader reads the fixture tree under testdata/tree.

func fixtureReader() *Reader {
	return &Reader{
		ProcRoot:   filepath.Join("testdata", "tree", "proc"),
		CgroupRoot: filepath.Join("testdata", "tree", "sys", "fs", "cgroup"),
		selfPID:    125,
	}
}

func readFixture(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "tree", rel))
	require.NoError(t, err)
	return b
}

func mustRead(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return b
}

func TestParseStat(t *testing.T) {
	t.Run("comm with spaces and parens", func(t *testing.T) {
		got, err := ParseStat(readFixture(t, "proc/123/stat"))
		require.NoError(t, err)
		assert.Equal(t, Stat{
			PID: 1234, Comm: "my (weird) app", State: 'S', PPID: 1, PGRP: 1234, Session: 1234,
			TTYNr: 0, TPGID: -1, Flags: 4194560, Minflt: 15000, Majflt: 42,
			Utime: 300, Stime: 120, Priority: 20, NumThreads: 8, Starttime: 5000,
			Vsize: 123456789, Rss: 2048,
		}, got)
	})

	for name, in := range map[string]string{
		"short":          "1234 (short) S 1 2 3",
		"no paren":       "1234 short S 1",
		"bad pid":        "x (a) S 1 1 1 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 1 1 1",
		"non numeric":    "1 (a) S 1 1 1 0 -1 0 0 0 0 0 xx 0 0 0 20 0 1 0 1 1 1",
		"bad state":      "1 (a) SS 1 1 1 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 1 1 1",
		"empty":          "",
		"unbalanced end": ") (",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseStat([]byte(in))
			require.ErrorIs(t, err, ErrMalformed)
		})
	}
}

func TestParseStatm(t *testing.T) {
	got, err := ParseStatm(readFixture(t, "proc/123/statm"))
	require.NoError(t, err)
	assert.Equal(t, Statm{Size: 2000, Resident: 600, Shared: 250, Text: 1000, Data: 3000}, got)

	_, err = ParseStatm([]byte("1 2 3"))
	require.ErrorIs(t, err, ErrMalformed)
	_, err = ParseStatm([]byte("1 2 3 4 5 6 x"))
	require.ErrorIs(t, err, ErrMalformed)
}

func TestParseSched(t *testing.T) {
	got, err := ParseSched(readFixture(t, "proc/123/sched"))
	require.NoError(t, err)
	assert.Equal(t, Sched{
		NRSwitches: 9000, NRVoluntarySwitches: 7000, NRInvoluntarySwitches: 2000,
		SEAvgLoadSum: 111, SEAvgUtilSum: 222, SEAvgLoadAvg: 3, SEAvgUtilAvg: 4,
	}, got, "fractional se.* times are skipped, not errors")

	sum, err := ParseSched([]byte("h\n--\nnr_switches : 2\nnr_switches : 3\n"))
	require.NoError(t, err)
	assert.Equal(t, uint64(5), sum.NRSwitches, "per-thread repeats are summed")

	_, err = ParseSched([]byte("only one line"))
	require.ErrorIs(t, err, ErrMalformed)
}

func TestParseLimits(t *testing.T) {
	got, err := ParseLimits(readFixture(t, "proc/123/limits"))
	require.NoError(t, err)
	assert.Equal(t, Limit{Name: "Max cpu time", Soft: Unlimited, Hard: Unlimited, Unit: "seconds"}, got.CPUTime)
	assert.Equal(t, Limit{Name: "Max open files", Soft: 1024, Hard: 1048576, Unit: "files"}, got.OpenFiles)
	assert.Equal(t, uint64(65536), got.LockedMemory.Soft)
	assert.Zero(t, got.Processes, "absent rows are zero")

	unl, err := ParseLimits(mustRead(t, "proc_limits_unlimited.txt"))
	require.NoError(t, err)
	assert.Equal(t, Unlimited, unl.OpenFiles.Soft)
	assert.Equal(t, uint64(1048576), unl.OpenFiles.Hard)

	missing, err := ParseLimits([]byte("Limit  Soft  Hard  Units\nMax cpu time  1  unlimited  seconds\n"))
	require.NoError(t, err)
	assert.Empty(t, missing.OpenFiles.Name)

	noUnit, err := ParseLimits([]byte("Limit  Soft  Hard  Units\nMax nice priority         0                    0\n"))
	require.NoError(t, err)
	assert.Equal(t, Limit{Name: "Max nice priority", Soft: 0, Hard: 0}, noUnit.NicePriority)

	_, err = ParseLimits([]byte("h\nMax open files  abc  def  files\n"))
	require.ErrorIs(t, err, ErrMalformed)
}

func TestParseMeminfoAndStatus(t *testing.T) {
	mi, err := ParseMeminfo(readFixture(t, "proc/meminfo"))
	require.NoError(t, err)
	assert.Equal(t, uint64(16384000*1024), mi.Total)
	assert.Equal(t, uint64(8192000*1024), mi.Available)
	assert.Equal(t, uint64(100000*1024), mi.Buffers)
	_, err = ParseMeminfo([]byte("MemFree: 1 kB\n"))
	require.ErrorIs(t, err, ErrMalformed)

	st, err := ParseStatus(readFixture(t, "proc/123/status"))
	require.NoError(t, err)
	assert.Equal(t, Status{
		VmSize: 190000 * 1024, VmRSS: 51200 * 1024, RssAnon: 30000 * 1024, RssFile: 20000 * 1024,
		RssShmem: 1200 * 1024, VmData: 120000 * 1024, VmStk: 132 * 1024, VmExe: 4096 * 1024,
		Threads: 8, VoluntaryCtxtSwitches: 1500, NonvoluntaryCtxtSwitches: 75,
	}, st)
	_, err = ParseStatus([]byte("Name: x\n"))
	require.ErrorIs(t, err, ErrMalformed)
}

func TestParseMemoryLimit(t *testing.T) {
	for in, want := range map[string]struct {
		limit uint64
		ok    bool
	}{
		"536870912\n":         {536870912, true},
		"max\n":               {0, false},
		"9223372036854771712": {0, false},
	} {
		limit, ok, err := ParseMemoryLimit([]byte(in))
		require.NoError(t, err, in)
		assert.Equal(t, want.limit, limit, in)
		assert.Equal(t, want.ok, ok, in)
	}
	_, _, err := ParseMemoryLimit([]byte("lots"))
	require.ErrorIs(t, err, ErrMalformed)
}

func TestParseCGroups(t *testing.T) {
	v2, err := ParseCGroups(readFixture(t, "proc/123/cgroup"))
	require.NoError(t, err)
	assert.Equal(t, CGroups{{ID: 0, Path: "/app"}}, v2)
	u, ok := v2.Unified()
	require.True(t, ok)
	assert.Equal(t, "/app", u.Path)

	v1, err := ParseCGroups(readFixture(t, "proc/124/cgroup"))
	require.NoError(t, err)
	assert.Equal(t, CGroups{
		{ID: 12, Name: "memory", Path: "/app"},
		{ID: 11, Name: "cpu", Path: "/app"},
		{ID: 11, Name: "cpuacct", Path: "/app"},
		{ID: 1, Name: "systemd", Path: "/user.slice"},
	}, v1)
	c, ok := v1.Lookup("cpu,cpuacct")
	require.True(t, ok)
	assert.Equal(t, 11, c.ID)
	_, ok = v1.Lookup("pids")
	assert.False(t, ok)
	_, ok = v1.Unified()
	assert.False(t, ok)

	_, err = ParseCGroups([]byte("garbage"))
	require.ErrorIs(t, err, ErrMalformed)
	_, err = ParseCGroups([]byte("x:cpu:/a"))
	require.ErrorIs(t, err, ErrMalformed)
}

func TestParseCPUMax(t *testing.T) {
	got, err := ParseCPUMax([]byte("150000 100000\n"))
	require.NoError(t, err)
	assert.Equal(t, CPUConfig{Quota: 150 * time.Millisecond, Period: 100 * time.Millisecond}, got)

	got, err = ParseCPUMax([]byte("max 50000\n"))
	require.NoError(t, err)
	assert.Equal(t, CPUConfig{Period: 50 * time.Millisecond}, got)

	got, err = ParseCPUMax([]byte("max\n"))
	require.NoError(t, err)
	assert.Equal(t, 100*time.Millisecond, got.Period, "default period")

	for _, in := range []string{"", "1 2 3", "x 100", "100 0", "-5 100"} {
		_, err := ParseCPUMax([]byte(in))
		require.ErrorIs(t, err, ErrMalformed, in)
	}
}
