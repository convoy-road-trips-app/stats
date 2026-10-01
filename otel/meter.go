package otel

import (
	"sync"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/embedded"
)

// Meter implements the OpenTelemetry Meter interface
type Meter struct {
	embedded.Meter // Embed to satisfy interface

	provider  *MeterProvider
	name      string
	version   string
	schemaURL string

	// Instruments created by this meter
	instruments map[string]instrument
	mu          sync.RWMutex
}

// instrument is a marker interface for all instrument types
type instrument interface {
	getName() string
}

// Int64Counter creates a new Int64Counter instrument
func (m *Meter) Int64Counter(name string, opts ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := "counter_int64_" + name
	if inst, ok := m.instruments[key]; ok {
		return inst.(*int64Counter), nil
	}

	cfg := metric.NewInt64CounterConfig(opts...)
	counter := &int64Counter{
		meter:       m,
		name:        name,
		description: cfg.Description(),
		unit:        cfg.Unit(),
	}

	m.instruments[key] = counter
	return counter, nil
}

// Float64Counter creates a new Float64Counter instrument
func (m *Meter) Float64Counter(name string, opts ...metric.Float64CounterOption) (metric.Float64Counter, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := "counter_float64_" + name
	if inst, ok := m.instruments[key]; ok {
		return inst.(*float64Counter), nil
	}

	cfg := metric.NewFloat64CounterConfig(opts...)
	counter := &float64Counter{
		meter:       m,
		name:        name,
		description: cfg.Description(),
		unit:        cfg.Unit(),
	}

	m.instruments[key] = counter
	return counter, nil
}

// Int64UpDownCounter creates a new Int64UpDownCounter instrument
func (m *Meter) Int64UpDownCounter(name string, opts ...metric.Int64UpDownCounterOption) (metric.Int64UpDownCounter, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := "updowncounter_int64_" + name
	if inst, ok := m.instruments[key]; ok {
		return inst.(*int64UpDownCounter), nil
	}

	cfg := metric.NewInt64UpDownCounterConfig(opts...)
	counter := &int64UpDownCounter{
		meter:       m,
		name:        name,
		description: cfg.Description(),
		unit:        cfg.Unit(),
	}

	m.instruments[key] = counter
	return counter, nil
}

// Float64UpDownCounter creates a new Float64UpDownCounter instrument
func (m *Meter) Float64UpDownCounter(name string, opts ...metric.Float64UpDownCounterOption) (metric.Float64UpDownCounter, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := "updowncounter_float64_" + name
	if inst, ok := m.instruments[key]; ok {
		return inst.(*float64UpDownCounter), nil
	}

	cfg := metric.NewFloat64UpDownCounterConfig(opts...)
	counter := &float64UpDownCounter{
		meter:       m,
		name:        name,
		description: cfg.Description(),
		unit:        cfg.Unit(),
	}

	m.instruments[key] = counter
	return counter, nil
}

// Int64Histogram creates a new Int64Histogram instrument
func (m *Meter) Int64Histogram(name string, opts ...metric.Int64HistogramOption) (metric.Int64Histogram, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := "histogram_int64_" + name
	if inst, ok := m.instruments[key]; ok {
		return inst.(*int64Histogram), nil
	}

	cfg := metric.NewInt64HistogramConfig(opts...)
	histogram := &int64Histogram{
		meter:       m,
		name:        name,
		description: cfg.Description(),
		unit:        cfg.Unit(),
	}

	m.instruments[key] = histogram
	return histogram, nil
}

// Float64Histogram creates a new Float64Histogram instrument
func (m *Meter) Float64Histogram(name string, opts ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := "histogram_float64_" + name
	if inst, ok := m.instruments[key]; ok {
		return inst.(*float64Histogram), nil
	}

	cfg := metric.NewFloat64HistogramConfig(opts...)
	histogram := &float64Histogram{
		meter:       m,
		name:        name,
		description: cfg.Description(),
		unit:        cfg.Unit(),
	}

	m.instruments[key] = histogram
	return histogram, nil
}

// Int64Gauge creates a new Int64Gauge instrument
func (m *Meter) Int64Gauge(name string, opts ...metric.Int64GaugeOption) (metric.Int64Gauge, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := "gauge_int64_" + name
	if inst, ok := m.instruments[key]; ok {
		return inst.(*int64Gauge), nil
	}

	cfg := metric.NewInt64GaugeConfig(opts...)
	gauge := &int64Gauge{
		meter:       m,
		name:        name,
		description: cfg.Description(),
		unit:        cfg.Unit(),
	}

	m.instruments[key] = gauge
	return gauge, nil
}

// Float64Gauge creates a new Float64Gauge instrument
func (m *Meter) Float64Gauge(name string, opts ...metric.Float64GaugeOption) (metric.Float64Gauge, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := "gauge_float64_" + name
	if inst, ok := m.instruments[key]; ok {
		return inst.(*float64Gauge), nil
	}

	cfg := metric.NewFloat64GaugeConfig(opts...)
	gauge := &float64Gauge{
		meter:       m,
		name:        name,
		description: cfg.Description(),
		unit:        cfg.Unit(),
	}

	m.instruments[key] = gauge
	return gauge, nil
}

// Int64ObservableCounter creates an instrument whose callbacks observe a
// cumulative int64 total; each collection exports the observed total.
func (m *Meter) Int64ObservableCounter(name string, opts ...metric.Int64ObservableCounterOption) (metric.Int64ObservableCounter, error) {
	cfg := metric.NewInt64ObservableCounterConfig(opts...)
	spec := observableSpec{
		key: "observable_counter_int64_" + name, name: name, description: cfg.Description(), unit: cfg.Unit(),
		kind: observableCounter, callbacks: int64Callbacks(cfg.Callbacks()),
	}
	return observableFor(m, spec, func(o *observable) *int64ObservableCounter { return &int64ObservableCounter{observable: o} })
}

// Float64ObservableCounter creates an instrument whose callbacks observe a
// cumulative float64 total; each collection exports the observed total.
func (m *Meter) Float64ObservableCounter(name string, opts ...metric.Float64ObservableCounterOption) (metric.Float64ObservableCounter, error) {
	cfg := metric.NewFloat64ObservableCounterConfig(opts...)
	spec := observableSpec{
		key: "observable_counter_float64_" + name, name: name, description: cfg.Description(), unit: cfg.Unit(),
		kind: observableCounter, callbacks: float64Callbacks(cfg.Callbacks()),
	}
	return observableFor(m, spec, func(o *observable) *float64ObservableCounter { return &float64ObservableCounter{observable: o} })
}

// Int64ObservableUpDownCounter creates an instrument whose callbacks observe an
// int64 total that may decrease; it is exported as a gauge, like the
// synchronous UpDownCounter.
func (m *Meter) Int64ObservableUpDownCounter(name string, opts ...metric.Int64ObservableUpDownCounterOption) (metric.Int64ObservableUpDownCounter, error) {
	cfg := metric.NewInt64ObservableUpDownCounterConfig(opts...)
	spec := observableSpec{
		key: "observable_updowncounter_int64_" + name, name: name, description: cfg.Description(), unit: cfg.Unit(),
		kind: observableUpDownCounter, callbacks: int64Callbacks(cfg.Callbacks()),
	}
	return observableFor(m, spec, func(o *observable) *int64ObservableUpDownCounter {
		return &int64ObservableUpDownCounter{observable: o}
	})
}

// Float64ObservableUpDownCounter creates an instrument whose callbacks observe a
// float64 total that may decrease; it is exported as a gauge, like the
// synchronous UpDownCounter.
func (m *Meter) Float64ObservableUpDownCounter(name string, opts ...metric.Float64ObservableUpDownCounterOption) (metric.Float64ObservableUpDownCounter, error) {
	cfg := metric.NewFloat64ObservableUpDownCounterConfig(opts...)
	spec := observableSpec{
		key: "observable_updowncounter_float64_" + name, name: name, description: cfg.Description(), unit: cfg.Unit(),
		kind: observableUpDownCounter, callbacks: float64Callbacks(cfg.Callbacks()),
	}
	return observableFor(m, spec, func(o *observable) *float64ObservableUpDownCounter {
		return &float64ObservableUpDownCounter{observable: o}
	})
}

// Int64ObservableGauge creates an instrument whose callbacks observe the current
// int64 value.
func (m *Meter) Int64ObservableGauge(name string, opts ...metric.Int64ObservableGaugeOption) (metric.Int64ObservableGauge, error) {
	cfg := metric.NewInt64ObservableGaugeConfig(opts...)
	spec := observableSpec{
		key: "observable_gauge_int64_" + name, name: name, description: cfg.Description(), unit: cfg.Unit(),
		kind: observableGauge, callbacks: int64Callbacks(cfg.Callbacks()),
	}
	return observableFor(m, spec, func(o *observable) *int64ObservableGauge { return &int64ObservableGauge{observable: o} })
}

// Float64ObservableGauge creates an instrument whose callbacks observe the
// current float64 value.
func (m *Meter) Float64ObservableGauge(name string, opts ...metric.Float64ObservableGaugeOption) (metric.Float64ObservableGauge, error) {
	cfg := metric.NewFloat64ObservableGaugeConfig(opts...)
	spec := observableSpec{
		key: "observable_gauge_float64_" + name, name: name, description: cfg.Description(), unit: cfg.Unit(),
		kind: observableGauge, callbacks: float64Callbacks(cfg.Callbacks()),
	}
	return observableFor(m, spec, func(o *observable) *float64ObservableGauge { return &float64ObservableGauge{observable: o} })
}

// RegisterCallback registers f to observe insts, which must be observable
// instruments of this Meter, on every collection until it is unregistered.
func (m *Meter) RegisterCallback(f metric.Callback, insts ...metric.Observable) (metric.Registration, error) {
	return m.registerCallback(f, insts)
}
