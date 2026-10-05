package otel

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	otelglobal "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/embedded"
	"go.opentelemetry.io/otel/trace"
)

// defaultCollectionInterval is how often observable callbacks run between
// ForceFlush calls, matching the runtime metrics collector default.
const defaultCollectionInterval = 10 * time.Second

var (
	// ErrNilCallback is returned when a nil callback is registered.
	ErrNilCallback = errors.New("observable callback is nil")
	// ErrForeignObservable is returned for an instrument that was not created by
	// this package, for example by another metric SDK.
	ErrForeignObservable = errors.New("observable instrument from a different implementation")
	// ErrObservableMeter is returned when RegisterCallback gets an instrument
	// created by another Meter; that instrument is not registered.
	ErrObservableMeter = errors.New("observable instrument created by another meter")
	// ErrUnregisteredObservable is reported when a callback observes an
	// instrument it was not registered with; the observation is dropped.
	ErrUnregisteredObservable = errors.New("observable instrument not registered for callback")
	// ErrCollectionInterval is returned for a non-positive collection interval.
	ErrCollectionInterval = errors.New("collection interval must be positive")
)

// callbackRegistry runs the registered observable callbacks of a MeterProvider,
// one collection at a time, on ForceFlush, on Shutdown and every interval.
type callbackRegistry struct {
	interval  time.Duration
	disabled  bool       // OTEL_SDK_DISABLED: callbacks are never registered or run
	collectMu sync.Mutex // serializes collections; callbacks never run concurrently

	mu        sync.Mutex // guards the fields below
	callbacks []registeredCallback
	nextID    uint64
	started   bool // the periodic loop was started
	closed    bool // Shutdown ran its final collection
	stop      chan struct{}
	done      chan struct{}
}

type registeredCallback struct {
	id  uint64
	run func(context.Context) error
}

func newCallbackRegistry(interval time.Duration) *callbackRegistry {
	return &callbackRegistry{interval: interval, stop: make(chan struct{}), done: make(chan struct{})}
}

// register adds run to every following collection and starts the periodic
// loop on first use; a disabled registry registers nothing. The returned
// function removes it; it is idempotent.
func (r *callbackRegistry) register(run func(context.Context) error) (unregister func()) {
	if r.disabled {
		return func() {}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	id := r.nextID
	r.callbacks = append(r.callbacks, registeredCallback{id: id, run: run})
	if !r.started && !r.closed {
		r.started = true
		go r.loop()
	}
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		for i, callback := range r.callbacks {
			if callback.id == id {
				r.callbacks = append(r.callbacks[:i:i], r.callbacks[i+1:]...)
				return
			}
		}
	}
}

// collect runs every registered callback with ctx and joins their errors. After
// Shutdown it does nothing.
func (r *callbackRegistry) collect(ctx context.Context) error {
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return nil
	}
	return r.invoke(ctx)
}

func (r *callbackRegistry) invoke(ctx context.Context) error {
	r.collectMu.Lock()
	defer r.collectMu.Unlock()
	r.mu.Lock()
	callbacks := append([]registeredCallback(nil), r.callbacks...)
	r.mu.Unlock()
	if len(callbacks) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("collect observable instruments: %w", err)
	}

	// Observations are not made under the collecting span, so they get no exemplar.
	ctx = trace.ContextWithSpanContext(ctx, trace.SpanContext{})
	errs := make([]error, 0, len(callbacks))
	for _, callback := range callbacks {
		errs = append(errs, callback.run(ctx))
	}
	return errors.Join(errs...)
}

// shutdown stops the periodic loop and runs one final collection with ctx.
func (r *callbackRegistry) shutdown(ctx context.Context) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	started := r.started
	r.mu.Unlock()
	if started {
		close(r.stop)
		select {
		case <-r.done:
		case <-ctx.Done():
			return fmt.Errorf("stop observable collection: %w", ctx.Err())
		}
	}
	return r.invoke(ctx)
}

// loop collects every interval until shutdown. Each collection is bounded by
// the interval; its errors go to the global OpenTelemetry error handler.
func (r *callbackRegistry) loop() {
	defer close(r.done)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), r.interval)
			if err := r.collect(ctx); err != nil {
				otelglobal.Handle(err)
			}
			cancel()
		}
	}
}

// registerCallback implements Meter.RegisterCallback with the OTel SDK rules:
// no instruments gives a no-op registration, a foreign instrument fails the
// call, and instruments of another meter are skipped with an error.
func (m *Meter) registerCallback(f metric.Callback, insts []metric.Observable) (metric.Registration, error) {
	if f == nil {
		return nil, ErrNilCallback
	}
	var err error
	allowed := make(map[*observable]struct{}, len(insts))
	for _, inst := range insts {
		own, ok := inst.(observableInstrument)
		if !ok {
			return nil, fmt.Errorf("%w: %T", ErrForeignObservable, inst)
		}
		if owner := own.base().meter; owner != m {
			err = errors.Join(err, fmt.Errorf("%w: %q from meter %q, registered with meter %q",
				ErrObservableMeter, own.getName(), owner.name, m.name))
			continue
		}
		allowed[own.base()] = struct{}{}
	}
	if len(allowed) == 0 {
		return noopRegistration{}, err
	}
	unregister := m.provider.observers.register(func(ctx context.Context) error {
		scope := &observeScope{ctx: ctx}
		return scope.finish(f(ctx, &multiObserver{allowed: allowed, scope: scope}))
	})
	return &registration{unregister: sync.OnceFunc(unregister)}, err
}

type registration struct {
	embedded.Registration
	unregister func()
}

// Unregister stops the callback from being called; repeated calls are no-ops.
func (r *registration) Unregister() error {
	r.unregister()
	return nil
}

type noopRegistration struct{ embedded.Registration }

func (noopRegistration) Unregister() error { return nil }
