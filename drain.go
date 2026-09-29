package stats

import (
	"context"
	"errors"
	"fmt"
)

// drainChunk bounds the size of one export while a worker drains the ring.
const drainChunk = 500

// flushRequest asks one worker to export its partial batch, the ring and the
// pending drop counters with the caller's context.
type flushRequest struct {
	ctx  context.Context
	stop bool         // the worker exits after acknowledging (Shutdown)
	done chan<- error // buffered for every worker, so an ack never blocks
}

// Flush exports every observation accepted before the call, plus pending drop
// counters, through all exporters with ctx. It returns once they have been
// exported, or with ctx's error once ctx is done. Export failures are returned.
func (p *Pipeline) Flush(ctx context.Context) error {
	select {
	case <-p.shutdownCh:
		return ErrClientClosed
	default:
	}
	if err := p.broadcast(ctx, false); err != nil {
		return fmt.Errorf("flush: %w", err)
	}
	return nil
}

// Shutdown stops accepting observations, drains the ring and every worker
// batch through the exporters with ctx, then shuts the exporters down. If ctx
// ends first, the error wraps ctx's error and undelivered observations are dropped.
func (p *Pipeline) Shutdown(ctx context.Context) error {
	var shutdownErr error
	p.shutdownOnce.Do(func() {
		close(p.shutdownCh)
		errs := []error{p.broadcast(ctx, true)}
		// Workers that did not get the stop request exit without exporting.
		p.cancel()
		if ctx.Err() == nil {
			errs = append(errs, p.waitWorkers(ctx))
		}
		for _, exporter := range p.exporters {
			if err := exporter.Shutdown(ctx); err != nil {
				errs = append(errs, fmt.Errorf("exporter %s shutdown: %w", exporter.Name(), err))
			}
		}
		if err := errors.Join(errs...); err != nil {
			shutdownErr = fmt.Errorf("pipeline shutdown: %w", err)
		}
	})
	return shutdownErr
}

// broadcast sends one flush request to every worker, so every partial batch is
// exported, and waits for all acknowledgements.
func (p *Pipeline) broadcast(ctx context.Context, stop bool) error {
	done := make(chan error, len(p.flushes))
	request := flushRequest{ctx: ctx, stop: stop, done: done}
	for _, flushes := range p.flushes {
		select {
		case flushes <- request:
		case <-ctx.Done():
			return ctx.Err()
		case <-p.ctx.Done():
			return ErrClientClosed
		}
	}
	errs := make([]error, 0, len(p.flushes))
	for range p.flushes {
		select {
		case err := <-done:
			errs = append(errs, err)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return errors.Join(errs...)
}

// drain exports batch, the ring and pending drop counters with ctx. It pops at
// most one ring capacity, so records arriving during the drain cannot starve
// it; they are exported later by the regular worker loop.
func (p *Pipeline) drain(ctx context.Context, batch []*Metric) error {
	var errs []error
	for popped := 0; popped < p.buffer.Cap() && ctx.Err() == nil; {
		items := p.buffer.PopBatch(drainChunk)
		if len(items) == 0 {
			break
		}
		popped += len(items)
		batch = p.appendPopped(batch, items)
		if len(batch) >= drainChunk {
			errs = append(errs, p.processBatch(ctx, batch))
			batch = batch[:0]
		}
	}
	batch = p.cardinality.appendDropCounters(batch)
	errs = append(errs, p.processBatch(ctx, batch))
	return errors.Join(errs...)
}

func (p *Pipeline) waitWorkers(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for workers: %w", ctx.Err())
	}
}
