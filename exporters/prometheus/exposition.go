package prometheus

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"strconv"
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

// WriteFamilies renders families in the Prometheus text exposition format
// (version 0.0.4) with one "# TYPE" line per family and no timestamps. Output
// is deterministic: families are ordered by name and series by their labels.
// The inputs are not modified. Nothing is written if a family is invalid.
func WriteFamilies(w io.Writer, families []Family) error {
	sorted := slices.Clone(families)
	slices.SortStableFunc(sorted, func(a, b Family) int { return strings.Compare(a.Name, b.Name) })

	var buf bytes.Buffer
	for _, f := range sorted {
		if err := writeFamily(&buf, f); err != nil {
			return err
		}
	}
	_, err := w.Write(buf.Bytes())
	return err
}

func writeFamily(buf *bytes.Buffer, f Family) error {
	switch f.Type {
	case models.MetricTypeCounter, models.MetricTypeGauge, models.MetricTypeHistogram:
	default:
		return fmt.Errorf("%w: %q has unsupported type %s", ErrInvalidFamily, f.Name, f.Type)
	}

	series := slices.Clone(f.Series)
	sort.SliceStable(series, func(i, j int) bool { return compareLabels(series[i].Labels, series[j].Labels) < 0 })

	buf.WriteString("# TYPE ")
	buf.WriteString(f.Name)
	buf.WriteByte(' ')
	buf.WriteString(f.Type.String())
	buf.WriteByte('\n')

	for _, s := range series {
		if f.Type != models.MetricTypeHistogram {
			writeSample(buf, f.Name, s.Labels, nil, formatValue(s.Value))
			continue
		}
		if err := writeHistogram(buf, f.Name, s); err != nil {
			return err
		}
	}
	return nil
}

func writeHistogram(buf *bytes.Buffer, name string, s Series) error {
	h := s.Histogram
	if h == nil {
		return fmt.Errorf("%w: histogram %q series has no data", ErrInvalidFamily, name)
	}
	if len(h.Counts) != len(h.Bounds)+1 {
		return fmt.Errorf("%w: histogram %q has %d bounds but %d counts",
			ErrInvalidFamily, name, len(h.Bounds), len(h.Counts))
	}
	for i := 1; i < len(h.Bounds); i++ {
		if !(h.Bounds[i] > h.Bounds[i-1]) {
			return fmt.Errorf("%w: histogram %q bounds are not strictly increasing", ErrInvalidFamily, name)
		}
	}

	var cum uint64
	for i, b := range h.Bounds {
		cum += h.Counts[i]
		le := Label{Name: "le", Value: strconv.FormatFloat(b, 'g', -1, 64)}
		writeSample(buf, name+"_bucket", s.Labels, &le, strconv.FormatUint(cum, 10))
	}
	cum += h.Counts[len(h.Bounds)]
	inf := Label{Name: "le", Value: "+Inf"}
	writeSample(buf, name+"_bucket", s.Labels, &inf, strconv.FormatUint(cum, 10))
	writeSample(buf, name+"_sum", s.Labels, nil, formatValue(h.Sum))
	writeSample(buf, name+"_count", s.Labels, nil, strconv.FormatUint(cum, 10))
	return nil
}

func writeSample(buf *bytes.Buffer, name string, labels []Label, extra *Label, value string) {
	buf.WriteString(name)
	if len(labels) > 0 || extra != nil {
		buf.WriteByte('{')
		for i, l := range labels {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeLabel(buf, l)
		}
		if extra != nil {
			if len(labels) > 0 {
				buf.WriteByte(',')
			}
			writeLabel(buf, *extra)
		}
		buf.WriteByte('}')
	}
	buf.WriteByte(' ')
	buf.WriteString(value)
	buf.WriteByte('\n')
}

func writeLabel(buf *bytes.Buffer, l Label) {
	buf.WriteString(l.Name)
	buf.WriteString(`="`)
	buf.WriteString(escapeLabelValue(l.Value))
	buf.WriteByte('"')
}

var labelValueEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

// escapeLabelValue escapes backslash, double quote and newline.
func escapeLabelValue(v string) string {
	return labelValueEscaper.Replace(v)
}

func formatValue(v float64) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

func compareLabels(a, b []Label) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := strings.Compare(a[i].Name, b[i].Name); c != 0 {
			return c
		}
		if c := strings.Compare(a[i].Value, b[i].Value); c != 0 {
			return c
		}
	}
	return len(a) - len(b)
}
