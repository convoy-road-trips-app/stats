package prometheus

import (
	"bytes"
	"strings"
	"testing"

	"github.com/convoy-road-trips-app/stats/models"
)

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
