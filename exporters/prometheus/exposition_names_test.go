package prometheus

import (
	"errors"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats/models"
)

func TestNormalizeMetricName(t *testing.T) {
	cases := map[string]string{
		"http.requests":  "http_requests",
		"a-b c":          "a_b_c",
		"ns:metric":      "ns:metric",
		"9lives":         "_9lives",
		"":               "_",
		"ünï":            "_n_",
		"already_fine_1": "already_fine_1",
	}
	for in, want := range cases {
		if got := NormalizeMetricName(in); got != want {
			t.Errorf("NormalizeMetricName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeLabelName(t *testing.T) {
	cases := map[string]string{
		"http.request.method": "http_request_method",
		"a:b":                 "a_b",
		"1st":                 "_1st",
		"":                    "_",
		"ok_1":                "ok_1",
	}
	for in, want := range cases {
		if got := NormalizeLabelName(in); got != want {
			t.Errorf("NormalizeLabelName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCounterFamilyName(t *testing.T) {
	cases := map[string]string{
		"http.requests":       "http_requests_total",
		"http_requests_total": "http_requests_total",
		"http.requests.total": "http_requests_total",
	}
	for in, want := range cases {
		if got := CounterFamilyName(in); got != want {
			t.Errorf("CounterFamilyName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExposedLabelsNormalizationAndOrder(t *testing.T) {
	labels, err := ExposedLabels("m", models.MetricTypeCounter, []attribute.KeyValue{
		attribute.String("http.request.method", "GET"),
		attribute.Int("a", 7),
		attribute.Bool("flag", true),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Label{
		{"a", "7"}, {"flag", "true"}, {"http_request_method", "GET"},
	}
	if len(labels) != len(want) {
		t.Fatalf("got %v", labels)
	}
	for i := range want {
		if labels[i] != want[i] {
			t.Errorf("labels[%d] = %v, want %v", i, labels[i], want[i])
		}
	}
}

func TestLeRenamedOnHistogramOnly(t *testing.T) {
	attrs := []attribute.KeyValue{attribute.String("le", "x"), attribute.String("__name__", "y")}

	h, err := ExposedLabels("m", models.MetricTypeHistogram, attrs)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{h[0].Name, h[1].Name}
	if names[0] != "___name__" || names[1] != "_le" {
		t.Errorf("histogram labels = %v", names)
	}

	c, err := ExposedLabels("m", models.MetricTypeCounter, attrs)
	if err != nil {
		t.Fatal(err)
	}
	if c[0].Name != "___name__" || c[1].Name != "le" {
		t.Errorf("counter labels = %v", c)
	}
}

func TestLabelCollisionWithinObservation(t *testing.T) {
	cases := []struct {
		name  string
		typ   models.MetricType
		attrs []attribute.KeyValue
		label string
	}{
		{"normalized", models.MetricTypeCounter, []attribute.KeyValue{
			attribute.String("http.request.method", "GET"),
			attribute.String("http_request_method", "POST"),
		}, "http_request_method"},
		{"le renamed", models.MetricTypeHistogram, []attribute.KeyValue{
			attribute.String("le", "1"),
			attribute.String("_le", "2"),
		}, "_le"},
	}
	for _, tc := range cases {
		_, err := ExposedLabels("my.metric", tc.typ, tc.attrs)
		if !errors.Is(err, ErrLabelCollision) {
			t.Fatalf("%s: err = %v, want ErrLabelCollision", tc.name, err)
		}
		if !strings.Contains(err.Error(), "my.metric") || !strings.Contains(err.Error(), tc.label) {
			t.Errorf("%s: error %q must name metric and label", tc.name, err)
		}
	}
}

func TestFamilyRegistryCollisions(t *testing.T) {
	r := NewFamilyRegistry()

	fam, err := r.Register("http.requests", models.MetricTypeCounter)
	if err != nil || fam != "http_requests_total" {
		t.Fatalf("Register = %q, %v", fam, err)
	}
	// Re-registering the same source and type is idempotent.
	if fam, err = r.Register("http.requests", models.MetricTypeCounter); err != nil || fam != "http_requests_total" {
		t.Fatalf("re-register = %q, %v", fam, err)
	}

	for _, tc := range []struct {
		name string
		src  string
		typ  models.MetricType
	}{
		{"different source dotted", "http.requests.total", models.MetricTypeCounter},
		{"different source same family", "http_requests", models.MetricTypeCounter},
		{"gauge equals counter family", "http_requests_total", models.MetricTypeGauge},
	} {
		_, err := r.Register(tc.src, tc.typ)
		if !errors.Is(err, ErrFamilyCollision) {
			t.Errorf("%s: err = %v, want ErrFamilyCollision", tc.name, err)
			continue
		}
		if !strings.Contains(err.Error(), tc.src) {
			t.Errorf("%s: error %q must name metric %q", tc.name, err, tc.src)
		}
	}
}

func TestFamilyRegistryHistogramSeriesCollision(t *testing.T) {
	r := NewFamilyRegistry()
	if _, err := r.Register("lat", models.MetricTypeHistogram); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{"lat_bucket", "lat_sum", "lat_count"} {
		if _, err := r.Register(src, models.MetricTypeGauge); !errors.Is(err, ErrFamilyCollision) {
			t.Errorf("%s: err = %v, want ErrFamilyCollision", src, err)
		}
	}
	// Histogram registered after a gauge owning its series name loses.
	r2 := NewFamilyRegistry()
	if _, err := r2.Register("lat_count", models.MetricTypeGauge); err != nil {
		t.Fatal(err)
	}
	if _, err := r2.Register("lat", models.MetricTypeHistogram); !errors.Is(err, ErrFamilyCollision) {
		t.Errorf("err = %v, want ErrFamilyCollision", err)
	}
	// Histogram vs gauge with the exact family name.
	if _, err := r.Register("lat", models.MetricTypeGauge); !errors.Is(err, ErrFamilyCollision) {
		t.Errorf("err = %v, want ErrFamilyCollision", err)
	}
	// Unrelated names coexist.
	if _, err := r.Register("other", models.MetricTypeGauge); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
