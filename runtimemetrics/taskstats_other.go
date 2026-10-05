//go:build !linux

package runtimemetrics

// Get is not supported on this platform.
func Get(pid int) (DelayInfo, error) {
	return DelayInfo{}, errTaskstatsUnsupported
}
