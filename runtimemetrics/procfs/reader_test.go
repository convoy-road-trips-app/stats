package procfs

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReaderProcFiles(t *testing.T) {
	r := fixtureReader()

	st, err := r.ReadStat(123)
	require.NoError(t, err)
	assert.Equal(t, uint64(300), st.Utime)

	sm, err := r.ReadStatm(123)
	require.NoError(t, err)
	assert.Equal(t, uint64(600), sm.Resident)

	sc, err := r.ReadSched(123)
	require.NoError(t, err)
	assert.Equal(t, uint64(7000), sc.NRVoluntarySwitches)

	lim, err := r.ReadLimits(123)
	require.NoError(t, err)
	assert.Equal(t, uint64(1024), lim.OpenFiles.Soft)

	status, err := r.ReadStatus(123)
	require.NoError(t, err)
	assert.Equal(t, uint64(8), status.Threads)

	mi, err := r.ReadMeminfo()
	require.NoError(t, err)
	assert.Equal(t, uint64(16384000*1024), mi.Total)

	n, err := r.OpenFileCount(123)
	require.NoError(t, err)
	assert.Equal(t, uint64(4), n, "pid other than self counts every entry")

	_, err = r.ReadStat(999)
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = r.OpenFileCount(999)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestReaderOpenFileCountSelfExcludesListingFD(t *testing.T) {
	r := fixtureReader()
	r.selfPID = 123
	n, err := r.OpenFileCount(123)
	require.NoError(t, err)
	assert.Equal(t, uint64(3), n)
}

func TestReadCPUConfig(t *testing.T) {
	r := fixtureReader()

	t.Run("cgroup v2", func(t *testing.T) {
		got, err := r.ReadCPUConfig(123)
		require.NoError(t, err)
		assert.Equal(t, CPUConfig{Quota: 150 * time.Millisecond, Period: 100 * time.Millisecond, Weight: 200}, got)
	})

	t.Run("cgroup v1", func(t *testing.T) {
		got, err := r.ReadCPUConfig(124)
		require.NoError(t, err)
		assert.Equal(t, CPUConfig{Quota: 50 * time.Millisecond, Period: 100 * time.Millisecond, Shares: 512}, got)
	})

	t.Run("cgroup v1 unlimited quota", func(t *testing.T) {
		got, err := r.ReadCPUConfig(126)
		require.NoError(t, err)
		assert.Equal(t, CPUConfig{Period: 100 * time.Millisecond, Shares: 1024}, got)
	})

	t.Run("self falls back to the cgroup mount root", func(t *testing.T) {
		got, err := r.ReadCPUConfig(125)
		require.NoError(t, err)
		assert.Equal(t, CPUConfig{Period: 100 * time.Millisecond, Weight: 100}, got)
	})

	t.Run("other pid with unreachable path has no cgroup", func(t *testing.T) {
		r2 := fixtureReader()
		r2.selfPID = 1
		_, err := r2.ReadCPUConfig(125)
		require.ErrorIs(t, err, ErrNoCPUCgroup)
	})

	t.Run("missing process", func(t *testing.T) {
		_, err := r.ReadCPUConfig(999)
		require.ErrorIs(t, err, os.ErrNotExist)
	})
}

func TestReadMemoryLimit(t *testing.T) {
	r := fixtureReader()
	for name, tc := range map[string]struct {
		pid  int
		want uint64
	}{
		"v2 numeric":                       {123, 536870912},
		"v1 numeric":                       {124, 268435456},
		"v1 unlimited falls to MemTotal":   {126, 16384000 * 1024},
		"v2 max at root falls to MemTotal": {125, 16384000 * 1024},
	} {
		got, err := r.ReadMemoryLimit(tc.pid)
		require.NoError(t, err, name)
		assert.Equal(t, tc.want, got, name)
	}
}
