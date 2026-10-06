package prometheus

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/convoy-road-trips-app/stats/models"
)

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
