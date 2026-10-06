package stats

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
)

// Report records the metrics described by the struct tags of v through r. v is
// a struct, a pointer to a struct, or a slice or array of either (also of
// interface values holding them); nil pointers and a nil v record nothing.
//
// Struct tags:
//
//   - metric:"name" on a value field names the metric. On a struct field (or a
//     pointer to one) it is a prefix: names are joined with ".", so a field
//     tagged metric:"func.calls" holding a field tagged metric:"count" reports
//     "func.calls.count". A struct field without a metric tag is traversed
//     without adding a prefix.
//   - type:"counter", type:"gauge" or type:"histogram" picks the metric type,
//     histogram by default.
//   - tag:"key" on a string field attaches its value as the attribute key to
//     the metrics of its struct and of every struct nested in it. A tag on a
//     nested struct overrides an inherited tag with the same key, even when its
//     value is empty. An empty string value is never attached, so an empty
//     override removes the inherited tag.
//
// Value fields may be bool (reported as 0 or 1), any int, uint or float width,
// uintptr, or time.Duration, which is reported in seconds. All values are
// recorded as float64, so integers are exact up to 2^53 and larger magnitudes
// lose precision. Unexported fields are read like exported ones. Fields without
// a metric or tag struct tag are ignored.
//
// A field that carries a metric or tag struct tag but has an unsupported kind (a
// channel, map, pointer to a number, time.Time, or a string metric, for
// example), an unknown type tag, or a struct type that contains itself makes
// Report return an error wrapping ErrUnsupportedReportField before anything
// from that value is recorded. The per-type field plan is computed once and
// cached.
//
// Metrics are recorded through r's Counter, Gauge and Histogram methods, so
// prefixes and context tags apply, and opts are applied to every metric after
// the struct's tags. Recording continues past a failed metric; the returned
// error joins every failure.
func Report(ctx context.Context, r Recorder, v any, opts ...MetricOption) error {
	if r == nil {
		return fmt.Errorf("%w: nil recorder", ErrInvalidConfig)
	}
	return reportValue(ctx, r, reflect.ValueOf(v), opts, 0)
}

// ReportAt is like Report but stamps every metric with t, as WithTimestamp
// does. t takes precedence over a WithTimestamp in opts.
func ReportAt(ctx context.Context, r Recorder, t time.Time, v any, opts ...MetricOption) error {
	all := make([]MetricOption, 0, len(opts)+1)
	all = append(all, opts...)
	all = append(all, WithTimestamp(t))
	return Report(ctx, r, v, all...)
}

// valueRecorder is the part of Recorder that Report records through.
type valueRecorder interface {
	Counter(ctx context.Context, name string, value float64, opts ...MetricOption) error
	Gauge(ctx context.Context, name string, value float64, opts ...MetricOption) error
	Histogram(ctx context.Context, name string, value float64, opts ...MetricOption) error
}

// maxReportDepth bounds slice-in-slice and pointer-in-interface nesting of the
// input, which can be cyclic at runtime even though struct types cannot be.
const maxReportDepth = 32

var (
	durationType = reflect.TypeFor[time.Duration]()
	timeType     = reflect.TypeFor[time.Time]()

	// reportPlans caches the *reportPlan (or its build error) per reflect.Type.
	reportPlans sync.Map
)

// reportValue unwraps pointers and interfaces, fans out over slices and arrays
// and reports each struct it reaches.
func reportValue(ctx context.Context, r valueRecorder, rv reflect.Value, opts []MetricOption, depth int) error {
	if depth > maxReportDepth {
		return fmt.Errorf("%w: input nested deeper than %d levels", ErrUnsupportedReportField, maxReportDepth)
	}
	for rv.IsValid() && (rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface) {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	if !rv.IsValid() {
		return nil
	}

	switch rv.Kind() {
	case reflect.Struct:
		plan, err := planFor(rv.Type())
		if err != nil {
			return err
		}
		return plan.report(ctx, r, rv, opts)
	case reflect.Slice, reflect.Array:
		var errs []error
		for i := range rv.Len() {
			if err := reportValue(ctx, r, rv.Index(i), opts, depth+1); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	default:
		return fmt.Errorf("%w: cannot report a value of type %s", ErrUnsupportedReportField, rv.Type())
	}
}

func (p *reportPlan) report(ctx context.Context, r valueRecorder, root reflect.Value, opts []MetricOption) error {
	var errs []error
	for i := range p.scopes {
		scope := &p.scopes[i]
		sv, ok := walk(root, scope.path)
		if !ok {
			continue
		}

		scopeOpts := opts
		if attrs := scope.attributes(root); len(attrs) > 0 {
			scopeOpts = make([]MetricOption, 0, len(opts)+1)
			scopeOpts = append(scopeOpts, withKeyValues(attrs))
			scopeOpts = append(scopeOpts, opts...)
		}

		for j := range scope.leaves {
			leaf := &scope.leaves[j]
			if err := leaf.record(ctx, r, sv.Field(leaf.index), scopeOpts); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// attributes resolves the scope's tags against root, skipping empty values.
func (s *reportScope) attributes(root reflect.Value) []attribute.KeyValue {
	if len(s.tags) == 0 {
		return nil
	}
	attrs := make([]attribute.KeyValue, 0, len(s.tags))
	for _, t := range s.tags {
		fv, ok := walk(root, t.path)
		if !ok {
			continue
		}
		if v := fv.String(); v != "" {
			attrs = append(attrs, attribute.String(t.key, v))
		}
	}
	return attrs
}

func (l *reportLeaf) record(ctx context.Context, r valueRecorder, fv reflect.Value, opts []MetricOption) error {
	value := l.value(fv)
	switch l.typ {
	case MetricTypeCounter:
		return r.Counter(ctx, l.name, value, opts...)
	case MetricTypeGauge:
		return r.Gauge(ctx, l.name, value, opts...)
	default:
		return r.Histogram(ctx, l.name, value, opts...)
	}
}

func (l *reportLeaf) value(fv reflect.Value) float64 {
	switch l.kind {
	case reflect.Bool:
		if fv.Bool() {
			return 1
		}
		return 0
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if l.secs {
			return time.Duration(fv.Int()).Seconds()
		}
		return float64(fv.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return float64(fv.Uint())
	default:
		return fv.Float()
	}
}
