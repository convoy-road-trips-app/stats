package stats

import (
	"fmt"
	"reflect"
	"slices"
)

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
