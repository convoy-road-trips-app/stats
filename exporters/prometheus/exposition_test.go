package prometheus

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats/models"
)

func render(t *testing.T, families ...Family) string {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteFamilies(&buf, families); err != nil {
		t.Fatalf("WriteFamilies: %v", err)
	}
	return buf.String()
}

func TestCounterExposition(t *testing.T) {
	out := render(t, Family{
		Name: CounterFamilyName("http.requests"),
		Type: models.MetricTypeCounter,
		Series: []Series{
			{Labels: []Label{{Name: "method", Value: "GET"}}, Value: 3},
		},
	})
	for _, want := range []string{
		"# TYPE http_requests_total counter\n",
		"http_requests_total{method=\"GET\"} 3\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Count(out, "# TYPE") != 1 {
		t.Errorf("want exactly one TYPE line:\n%s", out)
	}
}

func TestHistogramBucketsIncludesInf(t *testing.T) {
	out := render(t, Family{
		Name: "latency",
		Type: models.MetricTypeHistogram,
		Series: []Series{{
			Labels: []Label{{Name: "route", Value: "/a"}},
			Histogram: &HistogramData{
				Bounds: []float64{0.5, 1, 2.5},
				Counts: []uint64{1, 2, 3, 4},
				Sum:    12.5,
			},
		}},
	})
	want := `# TYPE latency histogram
latency_bucket{route="/a",le="0.5"} 1
latency_bucket{route="/a",le="1"} 3
latency_bucket{route="/a",le="2.5"} 6
latency_bucket{route="/a",le="+Inf"} 10
latency_sum{route="/a"} 12.5
latency_count{route="/a"} 10
`
	if out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestHistogramNoLabels(t *testing.T) {
	out := render(t, Family{
		Name: "h",
		Type: models.MetricTypeHistogram,
		Series: []Series{{Histogram: &HistogramData{
			Bounds: []float64{1}, Counts: []uint64{0, 2}, Sum: 4,
		}}},
	})
	for _, want := range []string{`h_bucket{le="1"} 0`, `h_bucket{le="+Inf"} 2`, "h_sum 4\n", "h_count 2\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestHistogramInvalid(t *testing.T) {
	cases := map[string]*HistogramData{
		"nil":            nil,
		"count mismatch": {Bounds: []float64{1, 2}, Counts: []uint64{1}},
		"unsorted":       {Bounds: []float64{2, 1}, Counts: []uint64{1, 1, 1}},
	}
	for name, h := range cases {
		var buf bytes.Buffer
		err := WriteFamilies(&buf, []Family{{
			Name: "h", Type: models.MetricTypeHistogram,
			Series: []Series{{Histogram: h}},
		}})
		if err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestLabelEscaping(t *testing.T) {
	out := render(t, Family{
		Name: "g",
		Type: models.MetricTypeGauge,
		Series: []Series{
			{Labels: []Label{{Name: "p", Value: "a\\b\"c\nd"}}, Value: 1.5},
		},
	})
	want := "g{p=\"a\\\\b\\\"c\\nd\"} 1.5\n"
	if !strings.Contains(out, want) {
		t.Errorf("want %q in:\n%s", want, out)
	}
}

func TestSpecialValues(t *testing.T) {
	inf := 1.0
	for range 2000 {
		inf *= 10
	}
	out := render(t, Family{
		Name: "g", Type: models.MetricTypeGauge,
		Series: []Series{
			{Labels: []Label{{Name: "k", Value: "a"}}, Value: inf},
			{Labels: []Label{{Name: "k", Value: "b"}}, Value: -inf},
			{Labels: []Label{{Name: "k", Value: "c"}}, Value: inf - inf},
		},
	})
	for _, want := range []string{`g{k="a"} +Inf`, `g{k="b"} -Inf`, `g{k="c"} NaN`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

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

func TestDeterministicSortedOutput(t *testing.T) {
	fams := []Family{
		{Name: "zeta", Type: models.MetricTypeGauge, Series: []Series{
			{Labels: []Label{{"k", "b"}}, Value: 2},
			{Labels: []Label{{"k", "a"}}, Value: 1},
			{Labels: nil, Value: 0},
		}},
		{Name: "alpha_total", Type: models.MetricTypeCounter, Series: []Series{
			{Labels: []Label{{"k", "x"}}, Value: 1},
		}},
	}
	want := `# TYPE alpha_total counter
alpha_total{k="x"} 1
# TYPE zeta gauge
zeta 0
zeta{k="a"} 1
zeta{k="b"} 2
`
	first := render(t, fams...)
	if first != want {
		t.Errorf("got:\n%s\nwant:\n%s", first, want)
	}
	// Reversed input must give identical output, and input must not be mutated.
	rev := []Family{fams[1], fams[0]}
	if got := render(t, rev...); got != want {
		t.Errorf("reversed input differs:\n%s", got)
	}
	if fams[0].Series[0].Labels[0].Value != "b" {
		t.Error("input series were reordered")
	}
}

func TestWriteFamiliesUnsupportedType(t *testing.T) {
	var buf bytes.Buffer
	err := WriteFamilies(&buf, []Family{{Name: "x", Type: models.MetricType(99)}})
	if err == nil {
		t.Fatal("want error")
	}
}
