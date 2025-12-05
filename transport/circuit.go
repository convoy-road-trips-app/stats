package transport

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// CircuitState represents the state of a circuit breaker
type CircuitState int32

const (
	// StateClosed means the circuit is closed and requests pass through
	StateClosed CircuitState = iota
	// StateOpen means the circuit is open and requests are rejected
	StateOpen
	// StateHalfOpen means the circuit is testing if it can close
	StateHalfOpen
)

// String returns the string representation of the circuit state
func (s CircuitState) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// CircuitBreaker implements the circuit breaker pattern to prevent cascading failures
type CircuitBreaker struct {
	// Configuration
	threshold      int           // Number of failures before opening
	timeout        time.Duration // Time to wait before attempting to close
	halfOpenMax    int           // Max requests in half-open state

	// State
	state          atomic.Int32  // CircuitState
	failures       atomic.Int32  // Consecutive failure count
	lastFailTime   atomic.Int64  // Unix nano timestamp of last failure
	halfOpenCount  atomic.Int32  // Number of requests in half-open state

	// Metrics
	totalSuccess   atomic.Uint64
	totalFailures  atomic.Uint64
	totalRejected  atomic.Uint64
	stateChanges   atomic.Uint64

	mu sync.RWMutex
}

// NewCircuitBreaker creates a new circuit breaker
func NewCircuitBreaker(threshold int, timeout time.Duration) *CircuitBreaker {
	if threshold <= 0 {
		threshold = 5
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	cb := &CircuitBreaker{
		threshold:   threshold,
		timeout:     timeout,
		halfOpenMax: 3, // Allow 3 test requests in half-open
	}
	cb.state.Store(int32(StateClosed))

	return cb
}

// Call executes the given function if the circuit is not open
func (cb *CircuitBreaker) Call(ctx context.Context, fn func() error) error {
	// Check if we can proceed
	if !cb.allowRequest() {
		cb.totalRejected.Add(1)
		return ErrCircuitOpen
	}

	// Execute the function
	err := fn()

	// Record the result
	if err != nil {
		cb.recordFailure()
		return err
	}

	cb.recordSuccess()
	return nil
}

// allowRequest determines if a request should be allowed
func (cb *CircuitBreaker) allowRequest() bool {
	state := CircuitState(cb.state.Load())

	switch state {
	case StateClosed:
		return true

	case StateOpen:
		// Check if timeout has elapsed
		lastFail := time.Unix(0, cb.lastFailTime.Load())
		if time.Since(lastFail) > cb.timeout {
			// Try to transition to half-open
			if cb.state.CompareAndSwap(int32(StateOpen), int32(StateHalfOpen)) {
				cb.stateChanges.Add(1)
				cb.halfOpenCount.Store(0)
			}
			return true
		}
		return false

	case StateHalfOpen:
		// Allow limited requests in half-open state
		count := cb.halfOpenCount.Add(1)
		return count <= int32(cb.halfOpenMax)

	default:
		return false
	}
}

// recordSuccess records a successful request
func (cb *CircuitBreaker) recordSuccess() {
	cb.totalSuccess.Add(1)
	state := CircuitState(cb.state.Load())

	if state == StateHalfOpen {
		// If we're in half-open and got a success, try to close
		if cb.state.CompareAndSwap(int32(StateHalfOpen), int32(StateClosed)) {
			cb.failures.Store(0)
			cb.stateChanges.Add(1)
		}
	} else if state == StateClosed {
		// Reset failure count on success
		cb.failures.Store(0)
	}
}

// recordFailure records a failed request
func (cb *CircuitBreaker) recordFailure() {
	cb.totalFailures.Add(1)
	cb.lastFailTime.Store(time.Now().UnixNano())

	state := CircuitState(cb.state.Load())
	failures := cb.failures.Add(1)

	if state == StateHalfOpen {
		// Failure in half-open state -> back to open
		if cb.state.CompareAndSwap(int32(StateHalfOpen), int32(StateOpen)) {
			cb.stateChanges.Add(1)
		}
	} else if state == StateClosed && failures >= int32(cb.threshold) {
		// Too many failures -> open the circuit
		if cb.state.CompareAndSwap(int32(StateClosed), int32(StateOpen)) {
			cb.stateChanges.Add(1)
		}
	}
}

// State returns the current state of the circuit breaker
func (cb *CircuitBreaker) State() CircuitState {
	return CircuitState(cb.state.Load())
}

// IsOpen returns true if the circuit is open
func (cb *CircuitBreaker) IsOpen() bool {
	return cb.State() == StateOpen
}

// Reset resets the circuit breaker to closed state
func (cb *CircuitBreaker) Reset() {
	cb.state.Store(int32(StateClosed))
	cb.failures.Store(0)
	cb.halfOpenCount.Store(0)
	cb.stateChanges.Add(1)
}

// Stats returns statistics about the circuit breaker
func (cb *CircuitBreaker) Stats() CircuitStats {
	return CircuitStats{
		State:         cb.State(),
		Failures:      int(cb.failures.Load()),
		TotalSuccess:  cb.totalSuccess.Load(),
		TotalFailures: cb.totalFailures.Load(),
		TotalRejected: cb.totalRejected.Load(),
		StateChanges:  cb.stateChanges.Load(),
	}
}

// CircuitStats contains statistics about a circuit breaker
type CircuitStats struct {
	State         CircuitState
	Failures      int
	TotalSuccess  uint64
	TotalFailures uint64
	TotalRejected uint64
	StateChanges  uint64
}

// ErrCircuitOpen is returned when the circuit breaker is open
var ErrCircuitOpen = &circuitOpenError{}

type circuitOpenError struct{}

func (e *circuitOpenError) Error() string {
	return "circuit breaker is open"
}
