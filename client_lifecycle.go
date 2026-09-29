package stats

import (
	"context"
	"fmt"
	"time"
)

// Flush exports every observation recorded before the call, including pending
// drop counters, and returns once it has been exported, or with ctx's error once
// ctx is done. Call it before a Lambda handler returns.
func (c *Client) Flush(ctx context.Context) error {
	c.mu.RLock()
	closed := c.closed
	c.mu.RUnlock()
	if closed {
		return ErrClientClosed
	}
	return c.pipeline.Flush(ctx)
}

// Shutdown gracefully shuts down the client
func (c *Client) Shutdown(ctx context.Context) error {
	var shutdownErr error

	c.shutdownOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()

		if c.collector != nil {
			if err := c.collector.Stop(ctx); err != nil {
				shutdownErr = fmt.Errorf("stop runtime collector: %w", err)
			}
		}

		// Shutdown pipeline
		if err := c.pipeline.Shutdown(ctx); err != nil {
			if shutdownErr == nil {
				shutdownErr = err
			}
		}
	})

	return shutdownErr
}

// Close closes the client with a default 5-second timeout
func (c *Client) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.Shutdown(ctx)
}
