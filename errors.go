package stats

import (
	"errors"

	"github.com/convoy-road-trips-app/stats/models"
)

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

	// ErrRateLimitExceeded is returned when rate limit is exceeded
	ErrRateLimitExceeded = errors.New("stats: rate limit exceeded")

	// ErrCardinalityLimit is returned when an observation would add a series beyond MaxCardinality
	ErrCardinalityLimit = errors.New("stats: metric cardinality limit exceeded")

	// ErrInvalidTagKey is returned when a tag key is not dot-separated identifier
	// segments, ^[a-zA-Z_][a-zA-Z0-9_]*(\.[a-zA-Z_][a-zA-Z0-9_]*)*$ (http.method is valid);
	// the observation is not recorded
	ErrInvalidTagKey = errors.New("stats: invalid tag key")

	// ErrUnsupportedReportField is returned by Report and ReportAt when the
	// value, or a field of it that carries a metric or tag struct tag, has a
	// kind that cannot be reported; nothing is recorded in that case
	ErrUnsupportedReportField = errors.New("stats: unsupported report field")

	// ErrDatadogNotConfigured is returned by Client.Event when the client has no
	// Datadog backend to send the event to
	ErrDatadogNotConfigured = errors.New("stats: datadog is not configured")

	// ErrEventTooLarge is returned by Client.Event when the serialized event is
	// larger than the Datadog BufferSize; the event is not sent
	ErrEventTooLarge = models.ErrEventTooLarge
)
