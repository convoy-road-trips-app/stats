package otel

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/embedded"

	"github.com/convoy-road-trips-app/stats"
)

// int64Counter implements metric.Int64Counter
type int64Counter struct {
	embedded.Int64Counter // Embed to satisfy interface

	meter       *Meter
	name        string
	description string
	unit        string
}

func (i *int64Counter) getName() string { return i.name }

func (i *int64Counter) Add(ctx context.Context, incr int64, opts ...metric.AddOption) {
	cfg := metric.NewAddConfig(opts)
	attrs := cfg.Attributes()

	// Convert to stats metric options
	statsOpts := convertAttributes(attrs)

	// Record to underlying stats client with context
	_ = i.meter.provider.client.Counter(ctx, i.name, float64(incr), statsOpts...)
}

// float64Counter implements metric.Float64Counter
type float64Counter struct {
	embedded.Float64Counter // Embed to satisfy interface

	meter       *Meter
	name        string
	description string
	unit        string
}

func (f *float64Counter) getName() string { return f.name }

func (f *float64Counter) Add(ctx context.Context, incr float64, opts ...metric.AddOption) {
	cfg := metric.NewAddConfig(opts)
	attrs := cfg.Attributes()

	statsOpts := convertAttributes(attrs)

	_ = f.meter.provider.client.Counter(ctx, f.name, incr, statsOpts...)
}

// int64UpDownCounter implements metric.Int64UpDownCounter
type int64UpDownCounter struct {
	embedded.Int64UpDownCounter // Embed to satisfy interface

	meter       *Meter
	name        string
	description string
	unit        string
}

func (i *int64UpDownCounter) getName() string { return i.name }

func (i *int64UpDownCounter) Add(ctx context.Context, incr int64, opts ...metric.AddOption) {
	cfg := metric.NewAddConfig(opts)
	attrs := cfg.Attributes()

	statsOpts := convertAttributes(attrs)

	// UpDownCounter can go negative, use Gauge for this
	_ = i.meter.provider.client.Gauge(ctx, i.name, float64(incr), statsOpts...)
}

// float64UpDownCounter implements metric.Float64UpDownCounter
type float64UpDownCounter struct {
	embedded.Float64UpDownCounter // Embed to satisfy interface

	meter       *Meter
	name        string
	description string
	unit        string
}

func (f *float64UpDownCounter) getName() string { return f.name }

func (f *float64UpDownCounter) Add(ctx context.Context, incr float64, opts ...metric.AddOption) {
	cfg := metric.NewAddConfig(opts)
	attrs := cfg.Attributes()

	statsOpts := convertAttributes(attrs)

	_ = f.meter.provider.client.Gauge(ctx, f.name, incr, statsOpts...)
}

// int64Histogram implements metric.Int64Histogram
type int64Histogram struct {
	embedded.Int64Histogram // Embed to satisfy interface

	meter       *Meter
	name        string
	description string
	unit        string
}

func (i *int64Histogram) getName() string { return i.name }

func (i *int64Histogram) Record(ctx context.Context, value int64, opts ...metric.RecordOption) {
	cfg := metric.NewRecordConfig(opts)
	attrs := cfg.Attributes()

	statsOpts := convertAttributes(attrs)

	_ = i.meter.provider.client.Histogram(ctx, i.name, float64(value), statsOpts...)
}

// float64Histogram implements metric.Float64Histogram
type float64Histogram struct {
	embedded.Float64Histogram // Embed to satisfy interface

	meter       *Meter
	name        string
	description string
	unit        string
}

func (f *float64Histogram) getName() string { return f.name }

func (f *float64Histogram) Record(ctx context.Context, value float64, opts ...metric.RecordOption) {
	cfg := metric.NewRecordConfig(opts)
	attrs := cfg.Attributes()

	statsOpts := convertAttributes(attrs)

	_ = f.meter.provider.client.Histogram(ctx, f.name, value, statsOpts...)
}

// int64Gauge implements metric.Int64Gauge
type int64Gauge struct {
	embedded.Int64Gauge // Embed to satisfy interface

	meter       *Meter
	name        string
	description string
	unit        string
}

func (i *int64Gauge) getName() string { return i.name }

func (i *int64Gauge) Record(ctx context.Context, value int64, opts ...metric.RecordOption) {
	cfg := metric.NewRecordConfig(opts)
	attrs := cfg.Attributes()

	statsOpts := convertAttributes(attrs)

	_ = i.meter.provider.client.Gauge(ctx, i.name, float64(value), statsOpts...)
}

// float64Gauge implements metric.Float64Gauge
type float64Gauge struct {
	embedded.Float64Gauge // Embed to satisfy interface

	meter       *Meter
	name        string
	description string
	unit        string
}

func (f *float64Gauge) getName() string { return f.name }

func (f *float64Gauge) Record(ctx context.Context, value float64, opts ...metric.RecordOption) {
	cfg := metric.NewRecordConfig(opts)
	attrs := cfg.Attributes()

	statsOpts := convertAttributes(attrs)

	_ = f.meter.provider.client.Gauge(ctx, f.name, value, statsOpts...)
}

// Helper function to convert OTel attribute.Set to stats attributes
func convertAttributes(attrSet attribute.Set) []stats.MetricOption {
	// Iterate over the attribute set
	opts := make([]stats.MetricOption, 0, attrSet.Len())
	iter := attrSet.Iter()
	for iter.Next() {
		attr := iter.Attribute()
		// Handle different attribute types
		switch attr.Value.Type() {
		case attribute.STRING:
			opts = append(opts, stats.WithAttribute(string(attr.Key), attr.Value.AsString()))
		case attribute.INT64:
			opts = append(opts, stats.WithAttribute(string(attr.Key), attr.Value.AsString()))
		case attribute.FLOAT64:
			opts = append(opts, stats.WithAttribute(string(attr.Key), attr.Value.AsString()))
		case attribute.BOOL:
			opts = append(opts, stats.WithAttribute(string(attr.Key), attr.Value.AsString()))
		default:
			opts = append(opts, stats.WithAttribute(string(attr.Key), attr.Value.AsString()))
		}
	}
	return opts
}
