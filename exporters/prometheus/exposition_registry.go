package prometheus

import (
	"fmt"

	"github.com/convoy-road-trips-app/stats/models"
)

type familyEntry struct {
	source string
	typ    models.MetricType
}

// FamilyRegistry applies the first-registered-wins rule to family names. It is
// not safe for concurrent use; callers serialize access.
type FamilyRegistry struct {
	families map[string]familyEntry // exposed family name -> first registration
	series   map[string]string      // exposed series name -> owning family name
}

// NewFamilyRegistry returns an empty registry.
func NewFamilyRegistry() *FamilyRegistry {
	return &FamilyRegistry{
		families: make(map[string]familyEntry),
		series:   make(map[string]string),
	}
}

func seriesNames(family string, typ models.MetricType) []string {
	if typ == models.MetricTypeHistogram {
		return []string{family + "_bucket", family + "_sum", family + "_count"}
	}
	return []string{family}
}

// Register claims the family for a metric with the given source name and type
// and returns its exposed family name. Registering the same source name and
// type again is a no-op. If a different registration already owns the family
// name, or a series name (_bucket, _sum, _count, _total) it would expose, the
// error wraps ErrFamilyCollision and names the metric; the first registration
// keeps the name.
func (r *FamilyRegistry) Register(source string, typ models.MetricType) (string, error) {
	family := NormalizeMetricName(source)
	if typ == models.MetricTypeCounter {
		family = CounterFamilyName(source)
	}

	if e, ok := r.families[family]; ok {
		if e.source == source && e.typ == typ {
			return family, nil
		}
		return "", fmt.Errorf("%w: metric %q (%s) conflicts with %q (%s) on family %q",
			ErrFamilyCollision, source, typ, e.source, e.typ, family)
	}
	if owner, ok := r.series[family]; ok {
		return "", fmt.Errorf("%w: metric %q (%s) conflicts with family %q on series %q",
			ErrFamilyCollision, source, typ, owner, family)
	}
	names := seriesNames(family, typ)
	for _, s := range names {
		if _, ok := r.families[s]; ok {
			return "", fmt.Errorf("%w: metric %q (%s) series %q is an existing family",
				ErrFamilyCollision, source, typ, s)
		}
		if owner, ok := r.series[s]; ok {
			return "", fmt.Errorf("%w: metric %q (%s) series %q is owned by family %q",
				ErrFamilyCollision, source, typ, s, owner)
		}
	}

	r.families[family] = familyEntry{source: source, typ: typ}
	for _, s := range names {
		r.series[s] = family
	}
	return family, nil
}
