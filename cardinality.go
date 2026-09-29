package stats

import (
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/otel/attribute"
)

// Limits from the telemetry spec (D10) and tag rules.
const (
	defaultMaxCardinality   = 2000
	maxLabelsPerObservation = 10
	maxLabelValueChars      = 256
	droppedLabelsMetric     = "telemetry_dropped_labels_total"
)

type dropReason int

const (
	dropInvalidKey dropReason = iota
	dropLabelLimit
	dropSeriesLimit
	dropReasonCount
)

// dropReasonNames is the bounded value set of the drop counter's only attribute.
var dropReasonNames = [dropReasonCount]string{
	dropInvalidKey:  "invalid_key",
	dropLabelLimit:  "label_limit",
	dropSeriesLimit: "series_limit",
}

// cardinalityLimiter validates tags and admits at most a fixed number of
// distinct attribute sets per metric name. The zero value is ready to use.
//
// Hot path for admitted series is two lock-free sync.Map lookups; a per-metric
// mutex is taken only to admit an unseen series, so there is no global lock.
type cardinalityLimiter struct {
	metrics sync.Map // metric name -> *metricSeries
	pending [dropReasonCount]atomic.Uint64
	total   atomic.Uint64
}

type metricSeries struct {
	admitted sync.Map   // attribute.Distinct -> struct{}; lock-free reads
	mu       sync.Mutex // serializes admission of unseen series
	count    int        // guarded by mu
}

// admit sanitizes m.Attributes in place (drops invalid keys, caps values,
// keeps the first 10 keys in lexical order) and reports whether m's series is
// within the limit. A limit <= 0 means the default of 2000.
func (l *cardinalityLimiter) admit(m *Metric, limit int) bool {
	kept := m.Attributes[:0]
	for _, kv := range m.Attributes {
		if !validTagKey(string(kv.Key)) {
			l.drop(dropInvalidKey, 1)
			continue
		}
		if kv.Value.Type() == attribute.STRING {
			kv = kv.Key.String(capTagValue(kv.Value.AsString()))
		}
		kept = append(kept, kv)
	}
	m.Attributes = kept

	set := attribute.NewSet(m.Attributes...) // sorted by key, duplicate keys collapsed
	if set.Len() > maxLabelsPerObservation {
		l.drop(dropLabelLimit, uint64(set.Len()-maxLabelsPerObservation))
		trimmed := set.ToSlice()[:maxLabelsPerObservation]
		m.Attributes = append(m.Attributes[:0], trimmed...)
		set = attribute.NewSet(trimmed...)
	}

	if limit <= 0 {
		limit = defaultMaxCardinality
	}
	entry, loaded := l.metrics.Load(m.Name)
	if !loaded {
		entry, _ = l.metrics.LoadOrStore(m.Name, &metricSeries{})
	}
	series := entry.(*metricSeries) // only *metricSeries is stored
	key := set.Equivalent()

	if _, known := series.admitted.Load(key); known {
		return true
	}

	series.mu.Lock()
	defer series.mu.Unlock()
	if _, known := series.admitted.Load(key); known {
		return true
	}
	if series.count >= limit {
		l.drop(dropSeriesLimit, 1)
		return false
	}
	series.admitted.Store(key, struct{}{})
	series.count++
	return true
}

func (l *cardinalityLimiter) drop(reason dropReason, n uint64) {
	l.pending[reason].Add(n)
	l.total.Add(n)
}

// appendDropCounters moves pending drop counts into counter observations.
// They are appended directly to an export batch and never pass through admit,
// so the drop counter cannot recursively count itself.
func (l *cardinalityLimiter) appendDropCounters(batch []*Metric) []*Metric {
	for reason := range dropReasonCount {
		n := l.pending[reason].Swap(0)
		if n == 0 {
			continue
		}
		m := AcquireMetric()
		m.Name = droppedLabelsMetric
		m.Type = MetricTypeCounter
		m.Value = float64(n)
		m.Timestamp = time.Now()
		m.Attributes = append(m.Attributes, attribute.String("reason", dropReasonNames[reason]))
		batch = append(batch, m)
	}
	return batch
}

// validTagKey reports whether key matches ^[a-zA-Z_][a-zA-Z0-9_]*$.
func validTagKey(key string) bool {
	if key == "" {
		return false
	}
	for i := range len(key) {
		c := key[i]
		switch {
		case c == '_', 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z':
		case '0' <= c && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// capTagValue truncates value to at most 256 characters (runes).
func capTagValue(value string) string {
	if len(value) <= maxLabelValueChars || utf8.RuneCountInString(value) <= maxLabelValueChars {
		return value
	}
	chars := 0
	for i := range value {
		if chars == maxLabelValueChars {
			return value[:i]
		}
		chars++
	}
	return value
}
