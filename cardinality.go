package stats

import (
	"fmt"
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
	dropLabelLimit dropReason = iota
	dropSeriesLimit
	dropReasonCount
)

// dropReasonNames is the bounded value set of the drop counter's only attribute.
// Only D10 drops are counted; invalid tag keys are rejected with ErrInvalidTagKey.
var dropReasonNames = [dropReasonCount]string{
	dropLabelLimit:  "label_limit",
	dropSeriesLimit: "series_limit",
}

// cardinalityLimiter validates tags and admits at most a fixed number of
// distinct attribute sets per metric name. The zero value is ready to use.
//
// Hot path for admitted series is two lock-free sync.Map lookups; a per-metric
// mutex is taken only to reserve, commit or release an unseen series, so there
// is no global lock.
type cardinalityLimiter struct {
	metrics sync.Map // metric name -> *metricSeries
	pending [dropReasonCount]atomic.Uint64
	total   atomic.Uint64
}

type metricSeries struct {
	admitted sync.Map   // attribute.Distinct -> struct{}; lock-free reads
	mu       sync.Mutex // guards count and reserved
	count    int        // admitted + reserved series
	reserved map[attribute.Distinct]int
}

// admission is the outcome of admit. An unseen series holds a reserved slot
// until commit (recorded) or release (recording failed).
type admission struct {
	limiter       *cardinalityLimiter
	series        *metricSeries // nil when the series was already admitted
	key           attribute.Distinct
	labelsDropped uint64
}

// admit rejects invalid tag keys, caps string values, keeps the first 10 keys
// in lexical order and reserves m's series within the limit (<= 0 means 2000).
// On success the caller must commit or release the returned admission.
func (l *cardinalityLimiter) admit(m *Metric, limit int) (admission, error) {
	for _, kv := range m.Attributes {
		if !validTagKey(string(kv.Key)) {
			return admission{}, fmt.Errorf("%w: %q", ErrInvalidTagKey, kv.Key)
		}
	}
	for i, kv := range m.Attributes {
		if kv.Value.Type() == attribute.STRING {
			m.Attributes[i] = kv.Key.String(capTagValue(kv.Value.AsString()))
		}
	}

	a := admission{limiter: l}
	set := attribute.NewSet(m.Attributes...) // sorted by key, duplicate keys collapsed
	if extra := set.Len() - maxLabelsPerObservation; extra > 0 {
		a.labelsDropped = uint64(extra)
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
	a.key = set.Equivalent()
	if _, known := series.admitted.Load(a.key); known {
		return a, nil
	}

	series.mu.Lock()
	defer series.mu.Unlock()
	if _, known := series.admitted.Load(a.key); known {
		return a, nil
	}
	if series.reserved == nil {
		series.reserved = make(map[attribute.Distinct]int)
	}
	if series.reserved[a.key] == 0 {
		if series.count >= limit {
			l.drop(dropSeriesLimit, 1)
			return admission{}, ErrCardinalityLimit
		}
		series.count++
	}
	series.reserved[a.key]++ // concurrent first observations share one slot
	a.series = series
	return a, nil
}

// admitAndEnqueue validates and admits m, then buffers it. A failed recording
// neither consumes a series slot nor counts trimmed labels.
func (p *Pipeline) admitAndEnqueue(m *Metric) error {
	admission, err := p.cardinality.admit(m, p.cfg.MaxCardinality)
	if err != nil {
		return err
	}
	if err := p.enqueue(m); err != nil {
		admission.release()
		return err
	}
	admission.commit()
	return nil
}

// commit marks the observation as recorded: the series becomes admitted and
// trimmed labels are counted.
func (a admission) commit() {
	if a.labelsDropped > 0 {
		a.limiter.drop(dropLabelLimit, a.labelsDropped)
	}
	if a.series == nil {
		return
	}
	a.series.mu.Lock()
	defer a.series.mu.Unlock()
	a.series.admitted.Store(a.key, struct{}{})
	a.series.unreserve(a.key)
}

// release returns a reserved slot after a failed recording, unless another
// observation of the same series was recorded meanwhile.
func (a admission) release() {
	if a.series == nil {
		return
	}
	a.series.mu.Lock()
	defer a.series.mu.Unlock()
	a.series.unreserve(a.key)
	if _, known := a.series.admitted.Load(a.key); !known && a.series.reserved[a.key] == 0 {
		a.series.count--
	}
}

func (s *metricSeries) unreserve(key attribute.Distinct) {
	s.reserved[key]--
	if s.reserved[key] == 0 {
		delete(s.reserved, key)
	}
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

// validTagKey reports whether key is one or more identifier segments joined by
// single dots, ^[a-zA-Z_][a-zA-Z0-9_]*(\.[a-zA-Z_][a-zA-Z0-9_]*)*$, so OTel
// semantic-convention keys such as http.method are accepted unchanged.
func validTagKey(key string) bool {
	segmentStart := 0
	for i := range len(key) {
		c := key[i]
		switch {
		case c == '_', 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z':
		case '0' <= c && c <= '9' && i > segmentStart:
		case c == '.' && i > segmentStart:
			segmentStart = i + 1
		default:
			return false
		}
	}
	return segmentStart < len(key)
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
