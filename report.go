package stats

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
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
func reportValue(ctx context.Context, r Recorder, rv reflect.Value, opts []MetricOption, depth int) error {
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

// reportTag locates a string field whose value becomes an attribute.
type reportTag struct {
	key  string
	path []int // from the root struct
}

// reportLeaf is one reportable value field of a scope's struct.
type reportLeaf struct {
	name  string
	typ   MetricType
	index int
	kind  reflect.Kind
	secs  bool // time.Duration, reported in seconds
}

// reportScope is one struct in the type tree: where it sits, the tags in
// effect for it, and its own value fields.
type reportScope struct {
	path   []int
	tags   []reportTag
	leaves []reportLeaf
}

// reportPlan is the flattened, immutable field plan of one struct type.
type reportPlan struct {
	scopes []reportScope
}

// planResult is what the cache stores, so build errors are cached too.
type planResult struct {
	plan *reportPlan
	err  error
}

func planFor(t reflect.Type) (*reportPlan, error) {
	if cached, ok := reportPlans.Load(t); ok {
		res := cached.(planResult)
		return res.plan, res.err
	}
	plan := &reportPlan{}
	err := plan.build(t, "", nil, nil, map[reflect.Type]bool{})
	if err != nil {
		plan = nil
	}
	actual, _ := reportPlans.LoadOrStore(t, planResult{plan: plan, err: err})
	res := actual.(planResult)
	return res.plan, res.err
}

func unsupported(t reflect.Type, f *reflect.StructField, why string) error {
	return fmt.Errorf("%w: %s.%s: %s", ErrUnsupportedReportField, t, f.Name, why)
}

// build appends t's scope and those of the structs nested in it to p.
func (p *reportPlan) build(t reflect.Type, prefix string, path []int, inherited []reportTag, visiting map[reflect.Type]bool) error {
	if visiting[t] {
		return fmt.Errorf("%w: %s contains itself", ErrUnsupportedReportField, t)
	}
	visiting[t] = true
	defer delete(visiting, t)

	// Tags first: they apply to every field of the struct regardless of order.
	tags, err := scopeTags(t, path, inherited)
	if err != nil {
		return err
	}

	scope := reportScope{path: path, tags: tags}
	type nested struct {
		t      reflect.Type
		prefix string
		path   []int
	}
	var children []nested

	for i := range t.NumField() {
		f := t.Field(i)
		if f.Tag.Get("tag") != "" {
			continue
		}
		name := f.Tag.Get("metric")
		if name == "-" {
			continue
		}

		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct && ft != timeType {
			children = append(children, nested{t: ft, prefix: joinName(prefix, name), path: appendPath(path, i)})
			continue
		}
		if name == "" {
			continue
		}

		leaf, err := newLeaf(t, &f, joinName(prefix, name))
		if err != nil {
			return err
		}
		leaf.index = i
		scope.leaves = append(scope.leaves, leaf)
	}

	if len(scope.leaves) > 0 {
		p.scopes = append(p.scopes, scope)
	}
	for _, c := range children {
		if err := p.build(c.t, c.prefix, c.path, tags, visiting); err != nil {
			return err
		}
	}
	return nil
}

// scopeTags returns the tags in effect for t: inherited ones, with t's own tag
// fields replacing those that share a key.
func scopeTags(t reflect.Type, path []int, inherited []reportTag) ([]reportTag, error) {
	tags := slices.Clone(inherited)
	for i := range t.NumField() {
		f := t.Field(i)
		key := f.Tag.Get("tag")
		if key == "" {
			continue
		}
		if f.Type.Kind() != reflect.String {
			return nil, unsupported(t, &f, "tag field must be a string")
		}
		if f.Tag.Get("metric") != "" {
			return nil, unsupported(t, &f, "field has both metric and tag")
		}
		rt := reportTag{key: key, path: appendPath(path, i)}
		if at := slices.IndexFunc(tags, func(x reportTag) bool { return x.key == key }); at >= 0 {
			tags[at] = rt
		} else {
			tags = append(tags, rt)
		}
	}
	return tags, nil
}

func newLeaf(t reflect.Type, f *reflect.StructField, name string) (reportLeaf, error) {
	leaf := reportLeaf{name: name, kind: f.Type.Kind()}
	switch f.Tag.Get("type") {
	case "", "histogram":
		leaf.typ = MetricTypeHistogram
	case "counter":
		leaf.typ = MetricTypeCounter
	case "gauge":
		leaf.typ = MetricTypeGauge
	default:
		return leaf, unsupported(t, f, fmt.Sprintf("unknown type %q", f.Tag.Get("type")))
	}

	switch leaf.kind {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		leaf.secs = f.Type == durationType
		return leaf, nil
	default:
		return leaf, unsupported(t, f, fmt.Sprintf("kind %s cannot be reported", leaf.kind))
	}
}

// appendPath returns a fresh copy of path with i appended.
func appendPath(path []int, i int) []int {
	out := make([]int, len(path)+1)
	copy(out, path)
	out[len(path)] = i
	return out
}

// walk follows path from rv through struct fields and pointers; ok is false
// when a nil pointer is on the way.
func walk(rv reflect.Value, path []int) (reflect.Value, bool) {
	for _, i := range path {
		rv = rv.Field(i)
		for rv.Kind() == reflect.Pointer {
			if rv.IsNil() {
				return rv, false
			}
			rv = rv.Elem()
		}
	}
	return rv, true
}

func (p *reportPlan) report(ctx context.Context, r Recorder, root reflect.Value, opts []MetricOption) error {
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

func (l *reportLeaf) record(ctx context.Context, r Recorder, fv reflect.Value, opts []MetricOption) error {
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
