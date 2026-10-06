//go:build linux

package runtimemetrics

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectProcInfoOtherProcessLinux(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sleep", "30")
	require.NoError(t, cmd.Start())
	defer func() { cancel(); _ = cmd.Wait() }()

	info, err := CollectProcInfo(cmd.Process.Pid)
	require.NoError(t, err)
	assert.Positive(t, info.Memory.Size)
	assert.Positive(t, info.Memory.Resident)
	assert.GreaterOrEqual(t, info.Memory.Size, info.Memory.Resident)
	assert.Equal(t, uint64(1), info.Threads.Num)
}
