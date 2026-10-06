//go:build darwin

package runtimemetrics

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectProcInfoDarwinSelfAndOther(t *testing.T) {
	info, err := CollectProcInfo(os.Getpid())
	require.NoError(t, err)
	assert.Positive(t, info.Memory.Resident)
	assert.Positive(t, info.Files.Max)

	_, err = CollectProcInfo(os.Getppid())
	require.Error(t, err)
	assert.True(t, IsUnsupported(err), "%v", err)
}
