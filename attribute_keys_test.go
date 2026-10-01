package stats

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

func TestValidTagKey_accepts_dot_separated_identifier_segments(t *testing.T) {
	cases := map[string]bool{
		// OTel semantic-convention keys and plain identifiers
		"http.method": true, "http.route": true, "http.response.status_code": true,
		"k8s.pod.name": true, "_a._b": true, "Route_2.x9": true, "method": true, "_": true,
		// malformed: empty segments, leading digits, characters outside [A-Za-z0-9_.]
		"": false, ".": false, "..": false, "bad..key": false, ".http.method": false, "http.method.": false,
		"http.1method": false, "1http.method": false, "http.-method": false, "http-method": false,
		"http.méthod": false, "http method": false, "http/method": false, "http:method": false, "http.method\x00": false,
	}
	for key, want := range cases {
		t.Run(key, func(t *testing.T) {
			require.Equal(t, want, validTagKey(key))
		})
	}
}

func TestRecord_accepts_dotted_OTel_semantic_attribute_keys(t *testing.T) {
	// Given
	p := newUnstartedPipeline(t, DefaultConfig())
	attrs := []attribute.KeyValue{
		attribute.String("http.method", "GET"),
		attribute.String("http.route", "/orders/{id}"),
		attribute.Int("http.response.status_code", 200),
	}

	// When
	err := p.Record(context.Background(), observation("http_requests_total", attrs...))

	// Then: keys reach the buffer exactly as given, nothing is counted as dropped
	require.NoError(t, err)
	require.ElementsMatch(t, attrs, bufferedAttributes(t, p))
	require.Zero(t, p.Stats().DroppedLabels)
}

func TestRecord_rejects_malformed_dotted_key_without_reserving_a_series(t *testing.T) {
	for _, key := range []string{"bad..key", ".http.method", "http.method.", "http.1method", "http.mé", "http. method"} {
		t.Run(key, func(t *testing.T) {
			// Given: room for exactly one series
			cfg := DefaultConfig()
			cfg.MaxCardinality = 1
			p := newUnstartedPipeline(t, cfg)

			// When
			err := p.Record(context.Background(), observation("http_requests_total",
				attribute.String("http.method", "GET"), attribute.String(key, "v")))

			// Then: typed error, nothing buffered, no drop accounting, and the slot is still free
			require.ErrorIs(t, err, ErrInvalidTagKey)
			require.Zero(t, p.buffer.Len())
			require.Zero(t, p.Stats().DroppedLabels)
			require.NoError(t, p.Record(context.Background(), observation("http_requests_total",
				attribute.String("http.method", "POST"))))
		})
	}
}

func TestRecord_trims_dotted_keys_to_first_10_lexical_keys(t *testing.T) {
	// Given: 12 dotted keys in non-lexical order ('.' sorts before '_' and letters)
	keys := []string{"x.l", "x.c", "x.k", "x.a", "x.j", "x.e", "x.b", "x.i", "x.d", "x.h", "x_a", "x.f"}
	attrs := make([]attribute.KeyValue, 0, len(keys))
	for _, k := range keys {
		attrs = append(attrs, attribute.String(k, "v"))
	}
	p := newUnstartedPipeline(t, DefaultConfig())

	// When
	require.NoError(t, p.Record(context.Background(), observation("hits_total", attrs...)))

	// Then
	retained := make([]string, 0, maxLabelsPerObservation)
	for _, kv := range bufferedAttributes(t, p) {
		retained = append(retained, string(kv.Key))
	}
	require.Equal(t, []string{"x.a", "x.b", "x.c", "x.d", "x.e", "x.f", "x.h", "x.i", "x.j", "x.k"}, retained)
	require.Equal(t, uint64(2), p.Stats().DroppedLabels)
}

func TestRecord_caps_value_of_dotted_key_at_256_characters(t *testing.T) {
	// Given
	p := newUnstartedPipeline(t, DefaultConfig())

	// When
	require.NoError(t, p.Record(context.Background(), observation("hits_total",
		attribute.String("url.path", strings.Repeat("é", 300)))))

	// Then
	got := bufferedAttributes(t, p)
	require.Len(t, got, 1)
	require.Equal(t, attribute.Key("url.path"), got[0].Key)
	require.Equal(t, 256, utf8.RuneCountInString(got[0].Value.AsString()))
}

func TestRecord_drops_dotted_key_series_beyond_default_2000(t *testing.T) {
	// Given: 2000 admitted series keyed by a dotted attribute
	p := newUnstartedPipeline(t, DefaultConfig())
	for i := range 2000 {
		require.NoError(t, p.Record(context.Background(), observation("jobs_total", attribute.Int("job.id", i))))
	}

	// When
	err := p.Record(context.Background(), observation("jobs_total", attribute.Int("job.id", 2000)))

	// Then
	require.ErrorIs(t, err, ErrCardinalityLimit)
	require.Equal(t, uint64(1), p.Stats().DroppedLabels)
	require.Equal(t, 2000, p.buffer.Len())
}
