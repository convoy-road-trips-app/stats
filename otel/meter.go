package otel

import (
	"fmt"
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

// Int64ObservableCounter creates a new async Int64ObservableCounter
// Note: Async/observable instruments are not yet supported due to OTel SDK limitations
func (m *Meter) Int64ObservableCounter(name string, opts ...metric.Int64ObservableCounterOption) (metric.Int64ObservableCounter, error) {
	return nil, fmt.Errorf("async/observable instruments not yet supported")
}

// Float64ObservableCounter creates a new async Float64ObservableCounter
// Note: Async/observable instruments are not yet supported due to OTel SDK limitations
func (m *Meter) Float64ObservableCounter(name string, opts ...metric.Float64ObservableCounterOption) (metric.Float64ObservableCounter, error) {
	return nil, fmt.Errorf("async/observable instruments not yet supported")
}

// Int64ObservableUpDownCounter creates a new async Int64ObservableUpDownCounter
// Note: Async/observable instruments are not yet supported due to OTel SDK limitations
func (m *Meter) Int64ObservableUpDownCounter(name string, opts ...metric.Int64ObservableUpDownCounterOption) (metric.Int64ObservableUpDownCounter, error) {
	return nil, fmt.Errorf("async/observable instruments not yet supported")
}

// Float64ObservableUpDownCounter creates a new async Float64ObservableUpDownCounter
// Note: Async/observable instruments are not yet supported due to OTel SDK limitations
func (m *Meter) Float64ObservableUpDownCounter(name string, opts ...metric.Float64ObservableUpDownCounterOption) (metric.Float64ObservableUpDownCounter, error) {
	return nil, fmt.Errorf("async/observable instruments not yet supported")
}

// Int64ObservableGauge creates a new async Int64ObservableGauge
// Note: Async/observable instruments are not yet supported due to OTel SDK limitations
func (m *Meter) Int64ObservableGauge(name string, opts ...metric.Int64ObservableGaugeOption) (metric.Int64ObservableGauge, error) {
	return nil, fmt.Errorf("async/observable instruments not yet supported")
}

// Float64ObservableGauge creates a new async Float64ObservableGauge
// Note: Async/observable instruments are not yet supported due to OTel SDK limitations
func (m *Meter) Float64ObservableGauge(name string, opts ...metric.Float64ObservableGaugeOption) (metric.Float64ObservableGauge, error) {
	return nil, fmt.Errorf("async/observable instruments not yet supported")
}

// RegisterCallback registers a callback for async instruments
// Note: Async/observable instruments are not yet supported due to OTel SDK limitations
func (m *Meter) RegisterCallback(f metric.Callback, insts ...metric.Observable) (metric.Registration, error) {
	return nil, fmt.Errorf("async/observable instruments not yet supported")
}
