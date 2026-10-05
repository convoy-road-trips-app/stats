package stats

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Flush exports every observation recorded before the call, including pending
// drop counters, and returns once it has been exported, or with ctx's error once
// ctx is done. Call it before a Lambda handler returns. On a view it flushes the
// root's pipeline.
func (c *Client) Flush(ctx context.Context) error {
	if c.core.disabled {
		return nil
	}
	if c.core.isClosed() {
		return ErrClientClosed
	}
	return c.core.pipeline.Flush(ctx)
}

// Shutdown gracefully shuts down the client. It stops the runtime collector
// and drains the pipeline even when the collector fails to stop, and reports
// both failures, joined with errors.Join, when both occur. On a view created by
// WithPrefix or WithTags it does nothing and returns nil: only the root client
// owns the pipeline.
func (c *Client) Shutdown(ctx context.Context) error {
	if !c.root {
		return nil
	}
	return c.core.shutdown(ctx)
}

func (core *clientCore) shutdown(ctx context.Context) error {
	if core.disabled {
		return nil
	}
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

// Close closes the client with a default 5-second timeout. On a view created by
// WithPrefix or WithTags it does nothing and returns nil.
func (c *Client) Close() error {
	if !c.root {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.Shutdown(ctx)
}
