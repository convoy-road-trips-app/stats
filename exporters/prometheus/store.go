package prometheus

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats/models"
)

// PullExporterName is the name a Handler reports from Name. It differs from
// the push exporter's name ("prometheus") so both can be registered together
// and their ExporterErrors stay separate.
const PullExporterName = "prometheus-pull"

// DefaultMetricTimeout is how long a series survives without an update when
// Handler.MetricTimeout is not set.
const DefaultMetricTimeout = 2 * time.Minute

// Handler is a cumulative in-memory metric store for Prometheus pull
// exposition. It implements models.Exporter: Export folds metrics into the
// store and WriteStats renders the current state in the text exposition
// format. It imports models only, never the root stats package.
//
// The exported fields must be set before the first use and not changed
// afterwards. The zero value is ready to use. A Handler must not be copied
// after first use.
//
// Series are keyed by family name plus the sorted, final exposed label set, so
// distinct source attribute sets that expose identical labels (for example
// "http.method" and "http_method") merge into one series. Counters accumulate,
// gauges keep the value with the newest Timestamp (ties keep the existing
// value) and histograms keep cumulative bucket counts, sum and count.
//
// The first type registered for a family name wins; a later metric that
// conflicts with it, and any observation whose attributes expose the same label
// name twice, is skipped and reported by Export as an error naming it. A
// family keeps its registration after all its series expired.
type Handler struct {
	// TrimPrefix is trimmed from the start of metric names before they are
	// normalized.
	TrimPrefix string
	// MetricTimeout removes series that were not updated for this long when
	// the store is scraped. Zero or negative means DefaultMetricTimeout.
	MetricTimeout time.Duration
	// Buckets returns the histogram bounds for a source metric name (before
	// TrimPrefix is applied). Nil, or an empty result, means
	// models.DefaultHistogramBuckets(). The bounds of a family are fixed by the
	// first histogram observation.
	Buckets func(name string) []float64

	// now is the clock; nil means time.Now. Tests inject their own.
	now func() time.Time

	mu       sync.Mutex
	registry *FamilyRegistry
	families map[string]*storeFamily
}

var _ models.Exporter = (*Handler)(nil)

type storeFamily struct {
	typ    models.MetricType
	bounds []float64 // histogram families only
	series map[string]*storeSeries
}

type storeSeries struct {
	labels    []Label
	value     float64 // counter total or newest gauge value
	gaugeTime time.Time
	counts    []uint64  // histogram: per-bucket counts, len(bounds)+1
	sum       float64   // histogram
	updated   time.Time // clock time of the last update, for expiry
}

// Name returns PullExporterName.
func (h *Handler) Name() string { return PullExporterName }

// Shutdown is a no-op; the store holds no resources.
func (h *Handler) Shutdown(context.Context) error { return nil }

func (h *Handler) clock() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

func (h *Handler) timeout() time.Duration {
	if h.MetricTimeout > 0 {
		return h.MetricTimeout
	}
	return DefaultMetricTimeout
}

func (h *Handler) bucketsFor(name string) []float64 {
	if h.Buckets != nil {
		if b := h.Buckets(name); len(b) > 0 {
			return slices.Clone(b)
		}
	}
	return models.DefaultHistogramBuckets()
}

// Export folds metrics into the store. Everything it needs is copied out of
// each metric; the pointers are not retained. Metrics that cannot be stored are
// skipped and reported in the returned (joined) error; the others are still
// applied.
func (h *Handler) Export(_ context.Context, metrics []*models.Metric) error {
	now := h.clock()

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.registry == nil {
		h.registry = NewFamilyRegistry()
		h.families = make(map[string]*storeFamily)
	}

	var errs []error
	for _, m := range metrics {
		if m == nil {
			continue
		}
		if err := h.observe(m, now); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// observe applies one metric. The caller holds h.mu.
func (h *Handler) observe(m *models.Metric, now time.Time) error {
	name := strings.TrimPrefix(m.Name, h.TrimPrefix)
	switch m.Type {
	case models.MetricTypeCounter, models.MetricTypeGauge, models.MetricTypeHistogram:
	default:
		return fmt.Errorf("prometheus: metric %q has unsupported type %s", m.Name, m.Type)
	}
	if m.Type == models.MetricTypeHistogram && math.IsNaN(m.Value) {
		return fmt.Errorf("prometheus: histogram %q observed NaN", m.Name)
	}

	// Validate labels first so a failed observation registers nothing.
	set := attribute.NewSet(m.Attributes...)
	labels, err := ExposedLabels(m.Name, m.Type, set.ToSlice())
	if err != nil {
		return err
	}
	famName, err := h.registry.Register(name, m.Type)
	if err != nil {
		return err
	}

	fam, err := h.family(famName, m)
	if err != nil {
		return err
	}

	key := labelsKey(labels)
	s := fam.series[key]
	if s == nil {
		s = &storeSeries{labels: labels}
		if m.Type == models.MetricTypeHistogram {
			s.counts = make([]uint64, len(fam.bounds)+1)
		}
		fam.series[key] = s
		if m.Type == models.MetricTypeGauge {
			s.value, s.gaugeTime = m.Value, m.Timestamp
		}
	}
	s.updated = now

	switch m.Type {
	case models.MetricTypeCounter:
		s.value += m.Value
	case models.MetricTypeGauge:
		if m.Timestamp.After(s.gaugeTime) {
			s.value, s.gaugeTime = m.Value, m.Timestamp
		}
	case models.MetricTypeHistogram:
		s.counts[sort.SearchFloat64s(fam.bounds, m.Value)]++
		s.sum += m.Value
	}
	return nil
}

// family returns the store family famName, creating it for m on first use. The
// caller holds h.mu.
func (h *Handler) family(famName string, m *models.Metric) (*storeFamily, error) {
	if fam := h.families[famName]; fam != nil {
		return fam, nil
	}
	fam := &storeFamily{typ: m.Type, series: make(map[string]*storeSeries)}
	if m.Type == models.MetricTypeHistogram {
		fam.bounds = h.bucketsFor(m.Name)
		if err := models.ValidateHistogramBuckets(fam.bounds); err != nil {
			return nil, fmt.Errorf("prometheus: histogram %q: %w", m.Name, err)
		}
	}
	h.families[famName] = fam
	return fam, nil
}

// labelsKey builds the series identity from labels sorted by name.
func labelsKey(labels []Label) string {
	var b strings.Builder
	for _, l := range labels {
		fmt.Fprintf(&b, "%d:%s=%d:%s;", len(l.Name), l.Name, len(l.Value), l.Value)
	}
	return b.String()
}

// WriteStats removes expired series and renders the current state in the
// Prometheus text exposition format, see WriteFamilies. The state is
// snapshotted under the lock and written after releasing it.
func (h *Handler) WriteStats(w io.Writer) error {
	return WriteFamilies(w, h.snapshot())
}

// snapshot expires stale series and returns a deep copy of the store as
// families. Families without series are omitted.
func (h *Handler) snapshot() []Family {
	cutoff := h.clock().Add(-h.timeout())

	h.mu.Lock()
	defer h.mu.Unlock()

	out := make([]Family, 0, len(h.families))
	for name, fam := range h.families {
		f := Family{Name: name, Type: fam.typ, Series: make([]Series, 0, len(fam.series))}
		for key, s := range fam.series {
			if s.updated.Before(cutoff) {
				delete(fam.series, key)
				continue
			}
			series := Series{Labels: s.labels, Value: s.value}
			if fam.typ == models.MetricTypeHistogram {
				series.Histogram = &HistogramData{
					Bounds: fam.bounds,
					Counts: slices.Clone(s.counts),
					Sum:    s.sum,
				}
			}
			f.Series = append(f.Series, series)
		}
		if len(f.Series) > 0 {
			out = append(out, f)
		}
	}
	return out
}
