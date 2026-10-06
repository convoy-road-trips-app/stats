//go:build linux

package procfs

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The real /proc is only present on Linux.
func TestLiveSelf(t *testing.T) {
	pid := os.Getpid()

	st, err := ReadStat(pid)
	require.NoError(t, err)
	assert.Equal(t, pid, st.PID)
	assert.Positive(t, st.NumThreads)

	sm, err := ReadStatm(pid)
	require.NoError(t, err)
	assert.Positive(t, sm.Size)

	lim, err := ReadLimits(pid)
	require.NoError(t, err)
	assert.NotZero(t, lim.OpenFiles.Soft)

	n, err := OpenFileCount(pid)
	require.NoError(t, err)
	assert.Positive(t, n)

	mi, err := ReadMeminfo()
	require.NoError(t, err)
	assert.Positive(t, mi.Total)

	_, err = ReadStatus(pid)
	require.NoError(t, err)
	_, err = ReadCGroups(pid)
	require.NoError(t, err)
	limit, err := ReadMemoryLimit(pid)
	require.NoError(t, err)
	assert.Positive(t, limit)
}
