//go:build !linux && !darwin

package runtimemetrics

import (
	"errors"
	"fmt"
)

func collectProcInfo(int) (ProcInfo, error) {
	return ProcInfo{}, fmt.Errorf("runtimemetrics: CollectProcInfo: %w", errors.ErrUnsupported)
}
