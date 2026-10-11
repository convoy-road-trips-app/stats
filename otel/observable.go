package otel

import (
	"context"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/embedded"

	"github.com/convoy-road-trips-app/stats"
)

// observableKind selects how observed values reach the stats pipeline.
type observableKind int

const (
	// observableCounter values are cumulative totals; the pipeline receives the
	// increase since the previous observation of the series as a counter.
	observableCounter observableKind = iota
	// observableUpDownCounter values are absolute totals, recorded as gauges
	// like the synchronous UpDownCounter.
	observableUpDownCounter
	// observableGauge values are recorded as gauges.
	observableGauge
)

// observable is the state shared by every observable instrument kind.
type observable struct {
	meter    *Meter
	name     string
	metadata []stats.MetricOption // description and unit, added to every record
	kind     observableKind

	mu     sync.Mutex                     // guards totals and orders counter records
	totals map[attribute.Distinct]float64 // last recorded total per counter series
}

func (o *observable) getName() string   { return o.name }
func (o *observable) base() *observable { return o }

// observableInstrument is implemented only by this package's observable
// instruments, so foreign metric.Observable values are told apart.
type observableInstrument interface {
	instrument
	base() *observable
}

// record sends one observation to the stats client. A counter series advances
// its total only when the record succeeds, so a failed record is included in
// the next increase.
func (o *observable) record(ctx context.Context, value float64, attrs attribute.Set) error {
	client := o.meter.provider.client
	opts := append(convertAttributes(attrs), o.metadata...)
	var err error
	switch o.kind {
	case observableCounter:
		o.mu.Lock()
		defer o.mu.Unlock()
		key := attrs.Equivalent()
		if err = client.Counter(ctx, o.name, value-o.totals[key], opts...); err == nil {
			if o.totals == nil {
				o.totals = make(map[attribute.Distinct]float64)
			}
			o.totals[key] = value
		}
	case observableUpDownCounter, observableGauge:
		err = client.Gauge(ctx, o.name, value, opts...)
	}
	if err != nil {
		return fmt.Errorf("observe %q: %w", o.name, err)
	}
	return nil
}

// instrumentCallback is an instrument-specific callback bound to its observer type.
type instrumentCallback func(ctx context.Context, scope *observeScope, inst *observable) error

// observableSpec describes an observable instrument to create.
type observableSpec struct {
	key         string // Meter.instruments key, unique per kind, number type and name
	name        string
	description string
	unit        string
	kind        observableKind
	callbacks   []instrumentCallback
}

// observableFor returns the instrument stored under spec.key or creates it with
// wrap and registers its callbacks. As in the OTel SDK, only the callbacks
// passed when the instrument is created are registered.
//
// Nil callbacks are rejected before the instrument is cached, so a failed
// first create cannot poison Meter.instruments and make a later create with a
// valid callback return a silent, unregistered instrument (see #4).
func observableFor[T observableInstrument](m *Meter, spec *observableSpec, wrap func(*observable) T) (T, error) {
	var zero T
	for _, callback := range spec.callbacks {
		if callback == nil {
			return zero, fmt.Errorf("%w: instrument %q", ErrNilCallback, spec.name)
		}
	}

	m.mu.Lock()
	if existing, ok := m.instruments[spec.key]; ok {
		m.mu.Unlock()
		return existing.(T), nil
	}
	inst := wrap(&observable{
		meter: m, name: spec.name, kind: spec.kind,
		metadata: []stats.MetricOption{stats.WithDescription(spec.description), stats.WithUnit(spec.unit)},
	})
	m.instruments[spec.key] = inst
	m.mu.Unlock()

	for _, callback := range spec.callbacks {
		m.provider.observers.register(func(ctx context.Context) error {
			scope := &observeScope{ctx: ctx}
			return scope.finish(callback(ctx, scope, inst.base()))
		})
	}
	return inst, nil
}

func int64Callbacks(callbacks []metric.Int64Callback) []instrumentCallback {
	bound := make([]instrumentCallback, len(callbacks))
	for i, callback := range callbacks {
		if callback != nil {
			bound[i] = func(ctx context.Context, scope *observeScope, inst *observable) error {
				return callback(ctx, &int64Observer{inst: inst, scope: scope})
			}
		}
	}
	return bound
}

func float64Callbacks(callbacks []metric.Float64Callback) []instrumentCallback {
	bound := make([]instrumentCallback, len(callbacks))
	for i, callback := range callbacks {
		if callback != nil {
			bound[i] = func(ctx context.Context, scope *observeScope, inst *observable) error {
				return callback(ctx, &float64Observer{inst: inst, scope: scope})
			}
		}
	}
	return bound
}

// The six observable instruments. The embedded metric.Int64Observable and
// metric.Float64Observable interfaces only supply the API's unexported marker
// methods and are never called.
type (
	int64ObservableCounter struct {
		embedded.Int64ObservableCounter
		metric.Int64Observable
		*observable
	}
	int64ObservableUpDownCounter struct {
		embedded.Int64ObservableUpDownCounter
		metric.Int64Observable
		*observable
	}
	int64ObservableGauge struct {
		embedded.Int64ObservableGauge
		metric.Int64Observable
		*observable
	}
	float64ObservableCounter struct {
		embedded.Float64ObservableCounter
		metric.Float64Observable
		*observable
	}
	float64ObservableUpDownCounter struct {
		embedded.Float64ObservableUpDownCounter
		metric.Float64Observable
		*observable
	}
	float64ObservableGauge struct {
		embedded.Float64ObservableGauge
		metric.Float64Observable
		*observable
	}
)
