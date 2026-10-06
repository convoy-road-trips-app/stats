package runtimemetrics

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/convoy-road-trips-app/stats/runtimemetrics/procfs"
)

// fixtureReader reads the procfs fixture tree, so these tests run on every
// platform.
func fixtureProcReader() *procfs.Reader {
	return &procfs.Reader{
		ProcRoot:   filepath.Join("procfs", "testdata", "tree", "proc"),
		CgroupRoot: filepath.Join("procfs", "testdata", "tree", "sys", "fs", "cgroup"),
	}
}

func TestCollectProcInfoFromProcfs(t *testing.T) {
	const pagesize = 4096
	info, err := collectProcInfoFrom(fixtureProcReader(), 123, pagesize)
	require.NoError(t, err)

	assert.Equal(t, CPUInfo{
		User: 3 * time.Second, Sys: 1200 * time.Millisecond,
		Period: 100 * time.Millisecond, Quota: 150 * time.Millisecond, Weight: 200,
	}, info.CPU)

	assert.Equal(t, MemoryInfo{
		Total:           536870912, // cgroup v2 memory.max of /app
		Available:       8192000 * 1024,
		Size:            2000 * pagesize,
		Resident:        600 * pagesize,
		Shared:          250 * pagesize,
		Text:            1000 * pagesize,
		Data:            3000 * pagesize,
		MajorPageFaults: 42,
		MinorPageFaults: 15000,
	}, info.Memory)

	assert.Equal(t, FileInfo{Open: 4, Max: 1024}, info.Files)
	assert.Equal(t, ThreadInfo{Num: 8, VoluntaryContextSwitches: 1500, InvoluntaryContextSwitches: 75}, info.Threads)
}

func TestCollectProcInfoV1CgroupAndMissingOptionalFiles(t *testing.T) {
	// pid 124 only has a cgroup file: stat is required and missing.
	_, err := collectProcInfoFrom(fixtureProcReader(), 124, 4096)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestCollectProcInfoMissingProcess(t *testing.T) {
	_, err := collectProcInfoFrom(fixtureProcReader(), 999, 4096)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestCollectProcInfoSelf(t *testing.T) {
	info, err := CollectProcInfo(os.Getpid())
	if IsUnsupported(err) {
		t.Skipf("unsupported platform: %v", err)
	}
	require.NoError(t, err)
	assert.Positive(t, info.Memory.Resident)
}

func TestCollectProcInfoUnsupportedIsDetectable(t *testing.T) {
	_, err := CollectProcInfo(-1)
	require.Error(t, err)
	// Either unsupported (non-Linux) or a read error for a bogus pid.
	assert.True(t, IsUnsupported(err) || errors.Is(err, os.ErrNotExist), "%v", err)
}
