//go:build linux

package runtimemetrics

import (
	"errors"
	"os"
	"syscall"
	"testing"
)

func TestGetSelf(t *testing.T) {
	info, err := Get(os.Getpid())
	if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.ENOENT) || IsUnsupported(err) {
		// TASKSTATS_CMD_GET needs CAP_NET_ADMIN, and some kernels lack taskstats.
		t.Skipf("taskstats unavailable: %v", err)
	}
	if err != nil {
		t.Fatalf("Get(self): %v", err)
	}
	if info.CPU < 0 || info.BlockIO < 0 || info.SwapIn < 0 || info.FreePages < 0 {
		t.Fatalf("Get(self) = %+v, want non-negative delays", info)
	}
	t.Logf("Get(self) = %+v", info)
}

func TestGetInvalidPID(t *testing.T) {
	for _, pid := range []int{0, -1} {
		if _, err := Get(pid); err == nil {
			t.Errorf("Get(%d): want error", pid)
		}
	}
}
