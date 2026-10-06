package prometheus

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats/models"
)

// Errors returned by the exposition helpers. They are wrapped with the
// offending metric (and label) name, so match them with errors.Is.
var (
	// ErrLabelCollision means two attributes of one observation map to the
	// same exposed label name.
	ErrLabelCollision = errors.New("prometheus: label name collision")
	// ErrFamilyCollision means a metric's family or series names clash with a
	// metric that was registered first.
	ErrFamilyCollision = errors.New("prometheus: metric family collision")
	// ErrInvalidFamily means a Family cannot be rendered.
	ErrInvalidFamily = errors.New("prometheus: invalid metric family")
)

// Label is one exposed label name/value pair. Names are expected to be
// already normalized, see ExposedLabels.
type Label struct {
	Name  string
	Value string
}

// HistogramData is the cumulative state of one histogram series.
type HistogramData struct {
	// Bounds are the strictly increasing finite upper bounds of the buckets.
	Bounds []float64
	// Counts are the per-bucket (non-cumulative) observation counts. It must
	// hold len(Bounds)+1 entries, the last one being the overflow bucket.
	Counts []uint64
	// Sum is the sum of all observed values.
	Sum float64
}

// Series is one labeled sample of a Family. Histogram must be set for
// histogram families and is ignored for the other types.
type Series struct {
	Labels    []Label
	Value     float64
	Histogram *HistogramData
}

// Family is a metric family ready to be rendered. Name is the final exposed
// family name (see NormalizeMetricName and CounterFamilyName).
type Family struct {
	Name   string
	Type   models.MetricType
	Series []Series
}

// NormalizeMetricName makes name a valid Prometheus metric name: every
// character outside [a-zA-Z0-9_:] becomes '_', and a leading digit or an empty
// name gets a '_' prefix.
func NormalizeMetricName(name string) string {
	return normalizeName(name, true)
}

// NormalizeLabelName makes name a valid Prometheus label name: every character
// outside [a-zA-Z0-9_] becomes '_', and a leading digit or an empty name gets
// a '_' prefix. Reserved-name handling lives in ExposedLabels.
func NormalizeLabelName(name string) string {
	return normalizeName(name, false)
}

func normalizeName(name string, allowColon bool) string {
	if name == "" {
		return "_"
	}
	var b strings.Builder
	b.Grow(len(name) + 1)
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			if i == 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r)
		case r == ':' && allowColon:
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// CounterFamilyName returns the exposed family name of a counter: the
// normalized name with a "_total" suffix unless it already has one.
func CounterFamilyName(name string) string {
	n := NormalizeMetricName(name)
	if strings.HasSuffix(n, "_total") {
		return n
	}
	return n + "_total"
}

// ExposedLabels converts the attributes of one observation of metric (the
// source name, used in errors) to exposed labels sorted by label name.
//
// Names are normalized with NormalizeLabelName. A name starting with "__", or
// "le" on a histogram, is reserved and gets a '_' prefix. If two attributes end
// up with the same exposed name, the error wraps ErrLabelCollision and names
// the metric and the label.
func ExposedLabels(metric string, typ models.MetricType, attrs []attribute.KeyValue) ([]Label, error) {
	labels := make([]Label, 0, len(attrs))
	for _, kv := range attrs {
		name := NormalizeLabelName(string(kv.Key))
		if strings.HasPrefix(name, "__") || (typ == models.MetricTypeHistogram && name == "le") {
			name = "_" + name
		}
		labels = append(labels, Label{Name: name, Value: kv.Value.String()})
	}
	slices.SortStableFunc(labels, func(a, b Label) int { return strings.Compare(a.Name, b.Name) })
	for i := 1; i < len(labels); i++ {
		if labels[i].Name == labels[i-1].Name {
			return nil, fmt.Errorf("%w: metric %q, label %q", ErrLabelCollision, metric, labels[i].Name)
		}
	}
	return labels, nil
}
