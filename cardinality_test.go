package stats

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats/transport"
)

// newUnstartedPipeline builds a pipeline whose buffer is drained by the test, not by workers.
func newUnstartedPipeline(t *testing.T, cfg *Config) *Pipeline {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &Pipeline{
		cfg:        cfg,
		buffer:     transport.NewRingBuffer(cfg.BufferSize),
		workers:    1,
		ctx:        ctx,
		cancel:     cancel,
		shutdownCh: make(chan struct{}),
	}
}

func observation(name string, attrs ...attribute.KeyValue) *Metric {
	m := AcquireMetric()
	m.Name = name
	m.Type = MetricTypeCounter
	m.Value = 1
	m.Attributes = append(m.Attributes, attrs...)
	return m
}

func bufferedAttributes(t *testing.T, p *Pipeline) []attribute.KeyValue {
	t.Helper()
	items := p.buffer.PopBatch(1)
	require.Len(t, items, 1)
	m, ok := items[0].(*Metric)
	require.True(t, ok)
	return m.Attributes
}

func TestRecord_drops_new_series_beyond_default_2000_when_admitted_series_continue(t *testing.T) {
	// Given: a default config and 2000 admitted series for one metric
	cfg := DefaultConfig()
	p := newUnstartedPipeline(t, cfg)
	for i := range 2000 {
		require.NoError(t, p.Record(context.Background(), observation("jobs_total", attribute.Int("id", i))))
	}

	// When: the 2001st distinct series arrives
	err := p.Record(context.Background(), observation("jobs_total", attribute.Int("id", 2000)))

	// Then: it is dropped and counted, while an admitted series and another metric still record
	require.ErrorIs(t, err, ErrCardinalityLimit)
	require.Equal(t, uint64(1), p.Stats().DroppedLabels)
	require.Equal(t, 2000, p.buffer.Len())
	require.NoError(t, p.Record(context.Background(), observation("jobs_total", attribute.Int("id", 7))))
	require.NoError(t, p.Record(context.Background(), observation("other_total", attribute.Int("id", 2000))))
}

func TestRecord_honors_configured_MaxCardinality(t *testing.T) {
	// Given
	cfg := DefaultConfig()
	WithMaxCardinality(2)(cfg)
	p := newUnstartedPipeline(t, cfg)
	require.NoError(t, p.Record(context.Background(), observation("jobs_total", attribute.String("k", "a"))))
	require.NoError(t, p.Record(context.Background(), observation("jobs_total", attribute.String("k", "b"))))

	// When
	err := p.Record(context.Background(), observation("jobs_total", attribute.String("k", "c")))

	// Then
	require.ErrorIs(t, err, ErrCardinalityLimit)
}

func TestRecord_treats_identical_attributes_in_different_order_as_one_series(t *testing.T) {
	// Given: room for exactly one series
	cfg := DefaultConfig()
	cfg.MaxCardinality = 1
	p := newUnstartedPipeline(t, cfg)
	require.NoError(t, p.Record(context.Background(), observation("hits_total",
		attribute.String("a", "1"), attribute.String("b", "2"))))

	// When: the same attribute set arrives in a different order
	err := p.Record(context.Background(), observation("hits_total",
		attribute.String("b", "2"), attribute.String("a", "1")))

	// Then
	require.NoError(t, err)
	require.Zero(t, p.Stats().DroppedLabels)
}

func TestRecord_rejects_observation_with_invalid_tag_key(t *testing.T) {
	for _, key := range []string{"http..method", "1abc", "", "a-b", "é"} {
		t.Run(key, func(t *testing.T) {
			// Given: room for exactly one series
			cfg := DefaultConfig()
			cfg.MaxCardinality = 1
			p := newUnstartedPipeline(t, cfg)

			// When: an observation carries one invalid key next to valid ones
			err := p.Record(context.Background(), observation("hits_total",
				attribute.String("route", "/x"), attribute.String(key, "v")))

			// Then: typed error, nothing buffered, no drop accounting, no series slot used
			require.ErrorIs(t, err, ErrInvalidTagKey)
			require.Zero(t, p.buffer.Len())
			require.Zero(t, p.Stats().DroppedLabels)
			require.NoError(t, p.Record(context.Background(), observation("hits_total", attribute.String("route", "/y"))))
		})
	}
}

func TestRecord_accepts_valid_tag_keys(t *testing.T) {
	// Given
	p := newUnstartedPipeline(t, DefaultConfig())

	// When
	err := p.Record(context.Background(), observation("hits.total",
		attribute.String("_ok9", "y"), attribute.String("Route_2", "/x")))

	// Then: dotted metric names stay allowed; only tag keys follow the identifier rule
	require.NoError(t, err)
	require.ElementsMatch(t, []attribute.KeyValue{
		attribute.String("_ok9", "y"), attribute.String("Route_2", "/x"),
	}, bufferedAttributes(t, p))
}

func TestRecord_releases_series_slot_when_memory_reservation_fails(t *testing.T) {
	// Given: one series slot and a memory limit too small for any metric
	cfg := DefaultConfig()
	cfg.MaxCardinality = 1
	cfg.MaxMemoryBytes = 1
	p := newUnstartedPipeline(t, cfg)
	require.ErrorIs(t, p.Record(context.Background(), observation("jobs_total", attribute.Int("id", 1))), ErrMemoryLimit)
	cfg.MaxMemoryBytes = DefaultConfig().MaxMemoryBytes

	// When: a different new series arrives once memory is available
	err := p.Record(context.Background(), observation("jobs_total", attribute.Int("id", 2)))

	// Then
	require.NoError(t, err)
	require.Zero(t, p.Stats().DroppedLabels)
}

func TestRecord_releases_series_slot_when_buffer_is_full(t *testing.T) {
	// Given: a two-slot buffer (the smallest ring) holding two admitted series, and room for three series
	cfg := DefaultConfig()
	cfg.MaxCardinality = 3
	cfg.BufferSize = 2
	p := newUnstartedPipeline(t, cfg)
	require.NoError(t, p.Record(context.Background(), observation("jobs_total", attribute.Int("id", 1))))
	require.NoError(t, p.Record(context.Background(), observation("jobs_total", attribute.Int("id", 2))))
	require.ErrorIs(t, p.Record(context.Background(), observation("jobs_total", attribute.Int("id", 3))), ErrBufferFull)
	require.Len(t, p.buffer.PopBatch(1), 1)

	// When: another new series arrives once the buffer has room
	err := p.Record(context.Background(), observation("jobs_total", attribute.Int("id", 4)))

	// Then: the rejected series 3 gave its slot back, so series 4 fits the limit of three
	require.NoError(t, err)
}

func TestRecord_counts_trimmed_labels_only_when_observation_is_recorded(t *testing.T) {
	// Given: a 12-label observation that fails its memory reservation
	cfg := DefaultConfig()
	cfg.MaxMemoryBytes = 1
	p := newUnstartedPipeline(t, cfg)

	// When
	err := p.Record(context.Background(), observation("hits_total", twelveLabels()...))

	// Then: the failed recording is not a D10 label drop
	require.ErrorIs(t, err, ErrMemoryLimit)
	require.Zero(t, p.Stats().DroppedLabels)
}

func TestRecord_counts_series_overflow_once_per_observation(t *testing.T) {
	// Given: the only series slot is taken
	cfg := DefaultConfig()
	cfg.MaxCardinality = 1
	p := newUnstartedPipeline(t, cfg)
	require.NoError(t, p.Record(context.Background(), observation("hits_total", attribute.String("a", "0"))))

	// When: a new 12-label series overflows
	err := p.Record(context.Background(), observation("hits_total", twelveLabels()...))

	// Then: one dropped series observation, not its trimmed labels as well
	require.ErrorIs(t, err, ErrCardinalityLimit)
	require.Equal(t, uint64(1), p.Stats().DroppedLabels)
}

func twelveLabels() []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, 12)
	for _, k := range []string{"l", "c", "k", "a", "j", "e", "b", "i", "d", "h", "g", "f"} {
		attrs = append(attrs, attribute.String(k, k))
	}
	return attrs
}

func TestRecord_caps_tag_values_at_256_characters(t *testing.T) {
	// Given: a multi-byte value of 300 characters
	p := newUnstartedPipeline(t, DefaultConfig())
	long := strings.Repeat("é", 300)

	// When
	require.NoError(t, p.Record(context.Background(), observation("hits_total", attribute.String("path", long))))

	// Then
	got := bufferedAttributes(t, p)
	require.Len(t, got, 1)
	value := got[0].Value.AsString()
	require.Equal(t, 256, utf8.RuneCountInString(value))
	require.Equal(t, strings.Repeat("é", 256), value)
	require.Zero(t, p.Stats().DroppedLabels)
}

func TestRecord_trims_to_first_10_lexical_keys_when_observation_has_12(t *testing.T) {
	// Given: 12 keys in non-lexical order
	p := newUnstartedPipeline(t, DefaultConfig())

	// When
	require.NoError(t, p.Record(context.Background(), observation("hits_total", twelveLabels()...)))

	// Then
	got := bufferedAttributes(t, p)
	retained := make([]string, 0, len(got))
	for _, kv := range got {
		retained = append(retained, string(kv.Key))
	}
	require.Equal(t, []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}, retained)
	require.Equal(t, uint64(2), p.Stats().DroppedLabels)
}

func TestRecord_admits_exactly_MaxCardinality_series_when_producers_race(t *testing.T) {
	// Given: 16 producers each offering 100 distinct series, sharing a 500-series budget
	cfg := DefaultConfig()
	cfg.MaxCardinality = 500
	p := newUnstartedPipeline(t, cfg)
	var admitted, rejected, unexpected atomic.Int64
	var wg sync.WaitGroup

	// When
	for g := range 16 {
		wg.Go(func() {
			for i := range 100 {
				err := p.Record(context.Background(), observation("race_total", attribute.Int("id", g*100+i)))
				switch {
				case err == nil:
					admitted.Add(1)
				case errors.Is(err, ErrCardinalityLimit):
					rejected.Add(1)
				default:
					unexpected.Add(1)
				}
			}
		})
	}
	wg.Wait()

	// Then
	require.Zero(t, unexpected.Load())
	require.Equal(t, int64(500), admitted.Load())
	require.Equal(t, int64(1100), rejected.Load())
	require.Equal(t, uint64(1100), p.Stats().DroppedLabels)
}

func TestRecord_shares_one_slot_when_producers_race_on_the_same_new_series(t *testing.T) {
	// Given: one series slot and 16 producers offering the same unseen series
	cfg := DefaultConfig()
	cfg.MaxCardinality = 1
	p := newUnstartedPipeline(t, cfg)
	var failed atomic.Int64
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if p.Record(context.Background(), observation("same_total", attribute.String("k", "v"))) != nil {
				failed.Add(1)
			}
		})
	}
	wg.Wait()

	// When: a different series arrives
	err := p.Record(context.Background(), observation("same_total", attribute.String("k", "other")))

	// Then: every racer recorded and exactly one slot was consumed
	require.Zero(t, failed.Load())
	require.ErrorIs(t, err, ErrCardinalityLimit)
}

type capturedPoint struct {
	name  string
	value float64
	attrs attribute.Set
}

func TestPipeline_emits_bounded_drop_counter_without_recursive_limiting(t *testing.T) {
	// Given: a started pipeline that admits one series and captures exports
	cfg := DefaultConfig()
	cfg.MaxCardinality = 1
	cfg.FlushInterval = 5 * time.Millisecond
	captured := make(chan capturedPoint, 64)
	p := newUnstartedPipeline(t, cfg)
	p.exporters = []Exporter{&MockExporter{name: "capture", exportFunc: func(_ context.Context, ms []*Metric) error {
		for _, m := range ms {
			captured <- capturedPoint{name: m.Name, value: m.Value, attrs: attribute.NewSet(m.Attributes...)}
		}
		return nil
	}}}
	p.exporterErrors = make([]atomic.Uint64, 1)
	require.NoError(t, p.Start())
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })

	// When: two series overflow, one observation is trimmed, one is rejected for an invalid key
	require.NoError(t, p.Record(context.Background(), observation("jobs_total", attribute.Int("id", 0))))
	for i := 1; i <= 2; i++ {
		require.ErrorIs(t, p.Record(context.Background(), observation("jobs_total", attribute.Int("id", i))), ErrCardinalityLimit)
	}
	require.NoError(t, p.Record(context.Background(), observation("wide_total", twelveLabels()...)))
	require.ErrorIs(t, p.Record(context.Background(), observation("jobs_total",
		attribute.Int("id", 0), attribute.String("bad..key", "x"))), ErrInvalidTagKey)

	// Then: drop counts arrive per bounded D10 reason; invalid-key rejections are errors, not drops
	totals := map[string]float64{}
	deadline := time.After(5 * time.Second)
	for totals["series_limit"] < 2 || totals["label_limit"] < 2 {
		select {
		case point := <-captured:
			if point.name != droppedLabelsMetric {
				continue
			}
			require.Equal(t, 1, point.attrs.Len(), "drop counter carries only the reason attribute")
			reason, _ := point.attrs.Value("reason")
			totals[reason.AsString()] += point.value
		case <-deadline:
			t.Fatalf("drop counter not exported; totals so far: %v", totals)
		}
	}
	require.Equal(t, map[string]float64{"series_limit": 2, "label_limit": 2}, totals)
}

func TestValidateConfig_rejects_negative_MaxCardinality(t *testing.T) {
	// Given
	cfg := DefaultConfig()
	cfg.MaxCardinality = -1

	// When
	err := ValidateConfig(cfg)

	// Then
	require.ErrorIs(t, err, ErrInvalidConfig)
}

func TestRecord_uses_default_limit_when_MaxCardinality_is_zero(t *testing.T) {
	// Given: a zero-value limit (for example a hand-built Config)
	cfg := DefaultConfig()
	cfg.MaxCardinality = 0
	p := newUnstartedPipeline(t, cfg)
	for i := range 2000 {
		require.NoError(t, p.Record(context.Background(), observation("jobs_total", attribute.Int("id", i))))
	}

	// When
	err := p.Record(context.Background(), observation("jobs_total", attribute.Int("id", 2000)))

	// Then
	require.ErrorIs(t, err, ErrCardinalityLimit)
}

// BenchmarkCardinalityAdmit_parallel measures the per-observation admission cost
// on the hot path (already-admitted series, concurrent producers).
func BenchmarkCardinalityAdmit_parallel(b *testing.B) {
	var limiter cardinalityLimiter
	routes := make([]attribute.KeyValue, 64)
	for i := range routes {
		routes[i] = attribute.String("route", fmt.Sprintf("/r/%d", i))
	}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		m := AcquireMetric()
		m.Name = "http_requests_total"
		i := 0
		for pb.Next() {
			m.Attributes = append(m.Attributes[:0], routes[i%len(routes)], attribute.String("method", "GET"))
			if a, err := limiter.admit(m, defaultMaxCardinality); err == nil {
				a.commit()
			}
			i++
		}
	})
}
