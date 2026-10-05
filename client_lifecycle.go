package stats

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Flush exports every observation recorded before the call, including pending
// drop counters, and returns once it has been exported, or with ctx's error once
// ctx is done. Call it before a Lambda handler returns.
func (c *Client) Flush(ctx context.Context) error {
	if c.core.isClosed() {
		return ErrClientClosed
	}
	return c.core.pipeline.Flush(ctx)
}

// Shutdown gracefully shuts down the client. It stops the runtime collector
// and drains the pipeline even when the collector fails to stop, and reports
// both failures, joined with errors.Join, when both occur.
func (c *Client) Shutdown(ctx context.Context) error {
	return c.core.shutdown(ctx)
}

func (core *clientCore) shutdown(ctx context.Context) error {
	var shutdownErr error

	core.shutdownOnce.Do(func() {
		core.mu.Lock()
		core.closed = true
		core.mu.Unlock()

		var collectorErr error
		if core.collector != nil {
			if err := core.collector.Stop(ctx); err != nil {
				collectorErr = fmt.Errorf("stop runtime collector: %w", err)
			}
		}

		pipelineErr := core.pipeline.Shutdown(ctx)
		shutdownErr = joinShutdownErrors(collectorErr, pipelineErr)
	})

	return shutdownErr
}

// joinShutdownErrors returns the one non-nil error unchanged, so a single
// failure keeps its own wrapping, and joins the errors when both failed.
func joinShutdownErrors(collectorErr, pipelineErr error) error {
	if collectorErr == nil {
		return pipelineErr
	}
	if pipelineErr == nil {
		return collectorErr
	}
	return errors.Join(collectorErr, pipelineErr)
}

// Close closes the client with a default 5-second timeout
func (c *Client) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.Shutdown(ctx)
}
