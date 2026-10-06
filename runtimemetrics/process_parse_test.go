package runtimemetrics

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return b
}

func TestParseProcStat(t *testing.T) {
	t.Run("comm with spaces and parens", func(t *testing.T) {
		got, err := parseProcStat(fixture(t, "proc_stat_parens.txt"))
		require.NoError(t, err)
		assert.Equal(t, procStat{
			utime: 300, stime: 120, minflt: 15000, majflt: 42, numThreads: 8,
		}, got)
	})

	t.Run("simple comm", func(t *testing.T) {
		got, err := parseProcStat(fixture(t, "proc_stat_simple.txt"))
		require.NoError(t, err)
		assert.Equal(t, procStat{
			utime: 10, stime: 5, minflt: 100, majflt: 3, numThreads: 1,
		}, got)
	})

	t.Run("short line", func(t *testing.T) {
		_, err := parseProcStat(fixture(t, "proc_stat_short.txt"))
		require.Error(t, err)
	})

	t.Run("no closing paren", func(t *testing.T) {
		_, err := parseProcStat([]byte("1 (abc S 1"))
		require.Error(t, err)
	})
}

func TestParseProcStatus(t *testing.T) {
	got := parseKeyValues(fixture(t, "proc_status.txt"))

	assert.Equal(t, uint64(51200*1024), got["VmRSS"])
	assert.Equal(t, uint64(20000*1024), got["RssFile"])
	assert.Equal(t, uint64(1200*1024), got["RssShmem"])
	assert.Equal(t, uint64(4096*1024), got["VmExe"])
	assert.Equal(t, uint64(120000*1024), got["VmData"])
	assert.Equal(t, uint64(1500), got["voluntary_ctxt_switches"])
	assert.Equal(t, uint64(75), got["nonvoluntary_ctxt_switches"])
	assert.NotContains(t, got, "Name")
	assert.NotContains(t, got, "Cpus_allowed_list")
}

func TestParseProcLimits(t *testing.T) {
	limit, unlimited, err := parseOpenFilesLimit(fixture(t, "proc_limits.txt"))
	require.NoError(t, err)
	assert.False(t, unlimited)
	assert.Equal(t, uint64(1024), limit)

	_, unlimited, err = parseOpenFilesLimit(fixture(t, "proc_limits_unlimited.txt"))
	require.NoError(t, err)
	assert.True(t, unlimited)

	_, _, err = parseOpenFilesLimit([]byte("Max cpu time  1  unlimited  seconds\n"))
	require.Error(t, err)
}

func TestParseMeminfo(t *testing.T) {
	got := parseKeyValues(fixture(t, "proc_meminfo.txt"))
	assert.Equal(t, uint64(16384000*1024), got["MemTotal"])
	assert.Equal(t, uint64(8192000*1024), got["MemAvailable"])
	assert.Equal(t, uint64(0), got["HugePages_Total"])
}

func TestParseCgroupMemoryMax(t *testing.T) {
	limit, ok := parseCgroupMemoryMax(fixture(t, "cgroup_memory_max_numeric.txt"))
	assert.True(t, ok)
	assert.Equal(t, uint64(536870912), limit)

	_, ok = parseCgroupMemoryMax(fixture(t, "cgroup_memory_max_max.txt"))
	assert.False(t, ok)

	_, ok = parseCgroupMemoryMax([]byte("garbage"))
	assert.False(t, ok)
}
