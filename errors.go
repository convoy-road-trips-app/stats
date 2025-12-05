package stats

import "errors"

// Sentinel errors for common failure modes
var (
	// ErrBufferFull is returned when the metric buffer is full and cannot accept new metrics
	ErrBufferFull = errors.New("stats: metric buffer is full")

	// ErrPoolClosed is returned when attempting to use a closed connection pool
	ErrPoolClosed = errors.New("stats: connection pool is closed")

	// ErrCircuitOpen is returned when the circuit breaker is open
	ErrCircuitOpen = errors.New("stats: circuit breaker is open")

	// ErrClientClosed is returned when attempting to use a closed client
	ErrClientClosed = errors.New("stats: client is closed")

	// ErrInvalidConfig is returned when the configuration is invalid
	ErrInvalidConfig = errors.New("stats: invalid configuration")

	// ErrExportFailed is returned when metric export fails
	ErrExportFailed = errors.New("stats: metric export failed")

	// ErrTimeout is returned when an operation times out
	ErrTimeout = errors.New("stats: operation timeout")

	// ErrMemoryLimit is returned when memory limit is exceeded
	ErrMemoryLimit = errors.New("stats: memory limit exceeded")
)
