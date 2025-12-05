package stats

import (
	"sync"
	"time"
)

// RateLimiter implements a token bucket rate limiter for backpressure control
// This prevents metric flooding and protects the system from DoS attacks
type RateLimiter struct {
	rate       float64 // tokens per second
	burst      int     // maximum burst size
	tokens     float64 // current tokens available
	lastUpdate time.Time
	mu         sync.Mutex
}

// NewRateLimiter creates a new token bucket rate limiter
// rate: tokens per second (metrics per second)
// burst: maximum burst size (maximum metrics that can be sent at once)
func NewRateLimiter(rate float64, burst int) *RateLimiter {
	if rate <= 0 {
		rate = 1000 // Default: 1000 metrics/sec
	}
	if burst <= 0 {
		burst = 100 // Default: 100 burst
	}

	return &RateLimiter{
		rate:       rate,
		burst:      burst,
		tokens:     float64(burst), // Start with full bucket
		lastUpdate: time.Now(),
	}
}

// Allow checks if a metric can be recorded given the rate limit
// Returns true if the metric is allowed, false if it should be dropped
func (rl *RateLimiter) Allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(rl.lastUpdate).Seconds()

	// Add tokens based on elapsed time
	rl.tokens += elapsed * rl.rate
	if rl.tokens > float64(rl.burst) {
		rl.tokens = float64(rl.burst) // Cap at burst size
	}

	rl.lastUpdate = now

	// Check if we have tokens available
	if rl.tokens >= 1.0 {
		rl.tokens -= 1.0
		return true
	}

	return false
}

// AllowN checks if N metrics can be recorded given the rate limit
// Returns true if all N metrics are allowed, false otherwise
func (rl *RateLimiter) AllowN(n int) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(rl.lastUpdate).Seconds()

	// Add tokens based on elapsed time
	rl.tokens += elapsed * rl.rate
	if rl.tokens > float64(rl.burst) {
		rl.tokens = float64(rl.burst)
	}

	rl.lastUpdate = now

	// Check if we have enough tokens
	tokensNeeded := float64(n)
	if rl.tokens >= tokensNeeded {
		rl.tokens -= tokensNeeded
		return true
	}

	return false
}

// Wait blocks until a token is available
// Returns immediately if a token is available
// Context can be used for cancellation
func (rl *RateLimiter) Wait() {
	for !rl.Allow() {
		// Calculate wait time
		rl.mu.Lock()
		tokensNeeded := 1.0 - rl.tokens
		waitTime := time.Duration(tokensNeeded/rl.rate*1000) * time.Millisecond
		rl.mu.Unlock()

		if waitTime > 0 {
			time.Sleep(waitTime)
		}
	}
}

// Stats returns current rate limiter statistics
func (rl *RateLimiter) Stats() RateLimiterStats {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Update tokens before reporting
	now := time.Now()
	elapsed := now.Sub(rl.lastUpdate).Seconds()
	tokens := rl.tokens + elapsed*rl.rate
	if tokens > float64(rl.burst) {
		tokens = float64(rl.burst)
	}

	return RateLimiterStats{
		Rate:            rl.rate,
		Burst:           rl.burst,
		AvailableTokens: tokens,
		Utilization:     1.0 - (tokens / float64(rl.burst)),
	}
}

// RateLimiterStats contains statistics about the rate limiter
type RateLimiterStats struct {
	Rate            float64 // Tokens per second
	Burst           int     // Maximum burst size
	AvailableTokens float64 // Current tokens available
	Utilization     float64 // 0.0 (idle) to 1.0 (fully utilized)
}
