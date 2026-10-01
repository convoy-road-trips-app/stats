package otel

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/embedded"
)

// observeScope is one callback invocation. Observers record into it and keep
// the errors of rejected observations; Observe may be called concurrently.
type observeScope struct {
	ctx  context.Context
	mu   sync.Mutex
	errs []error
}

func (s *observeScope) observe(inst *observable, value float64, opts []metric.ObserveOption) {
	cfg := metric.NewObserveConfig(opts)
	s.fail(inst.record(s.ctx, value, cfg.Attributes()))
}

func (s *observeScope) fail(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errs = append(s.errs, err)
}

// finish joins the callback's own error with the rejected observations.
func (s *observeScope) finish(callbackErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if callbackErr != nil {
		callbackErr = fmt.Errorf("observable callback: %w", callbackErr)
	}
	return errors.Join(append([]error{callbackErr}, s.errs...)...)
}

// int64Observer observes the single instrument of an instrument callback.
type int64Observer struct {
	embedded.Int64Observer
	inst  *observable
	scope *observeScope
}

func (o *int64Observer) Observe(value int64, opts ...metric.ObserveOption) {
	o.scope.observe(o.inst, float64(value), opts)
}

// float64Observer observes the single instrument of an instrument callback.
type float64Observer struct {
	embedded.Float64Observer
	inst  *observable
	scope *observeScope
}

func (o *float64Observer) Observe(value float64, opts ...metric.ObserveOption) {
	o.scope.observe(o.inst, value, opts)
}

// multiObserver observes the instruments a Meter.RegisterCallback callback was
// registered with. Observations of any other instrument are dropped and reported.
type multiObserver struct {
	embedded.Observer
	allowed map[*observable]struct{}
	scope   *observeScope
}

func (o *multiObserver) ObserveInt64(inst metric.Int64Observable, value int64, opts ...metric.ObserveOption) {
	o.observe(inst, float64(value), opts)
}

func (o *multiObserver) ObserveFloat64(inst metric.Float64Observable, value float64, opts ...metric.ObserveOption) {
	o.observe(inst, value, opts)
}

func (o *multiObserver) observe(inst metric.Observable, value float64, opts []metric.ObserveOption) {
	own, ok := inst.(observableInstrument)
	if !ok {
		o.scope.fail(fmt.Errorf("%w: %T", ErrForeignObservable, inst))
		return
	}
	if _, registered := o.allowed[own.base()]; !registered {
		o.scope.fail(fmt.Errorf("%w: %q", ErrUnregisteredObservable, own.getName()))
		return
	}
	o.scope.observe(own.base(), value, opts)
}
