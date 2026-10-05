package runtimemetrics

import (
	"errors"
	"fmt"
	"time"
)

// DelayInfo holds cumulative scheduler and resource delay totals for a
// process, as reported by the Linux taskstats interface.
type DelayInfo struct {
	// CPU is the time spent runnable but waiting for a CPU.
	CPU time.Duration
	// BlockIO is the time spent waiting for synchronous block I/O.
	BlockIO time.Duration
	// SwapIn is the time spent waiting for swap-in.
	SwapIn time.Duration
	// FreePages is the time spent waiting for memory reclaim. It is zero on
	// kernels whose taskstats version predates the field.
	FreePages time.Duration
}

// errTaskstatsUnsupported is returned by Get on platforms without taskstats.
var errTaskstatsUnsupported = fmt.Errorf("taskstats: %w", errors.ErrUnsupported)

// errTaskstatsShort reports a truncated netlink message, attribute or
// Taskstats payload.
var errTaskstatsShort = errors.New("taskstats: short or malformed data")

// IsUnsupported reports whether err means delay metrics are unavailable on
// this platform.
func IsUnsupported(err error) bool {
	return errors.Is(err, errTaskstatsUnsupported) || errors.Is(err, errors.ErrUnsupported)
}
