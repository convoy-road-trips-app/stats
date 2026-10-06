package httpstats_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats"
	"github.com/convoy-road-trips-app/stats/models"
	"github.com/convoy-road-trips-app/stats/statstest"
)

// serve sends a request with the given method and target through h and returns
// the metrics the client exported.
func serve(t *testing.T, build func(stats.Recorder) http.Handler, req func(*testing.T) *http.Request) []models.Metric {
	t.Helper()
	client, exp := statstest.NewClient(t)
	h := build(client)
	h.ServeHTTP(httptest.NewRecorder(), req(t))
	statstest.Flush(t, client)
	return exp.Metrics()
}

// newReq builds a request with a context, as noctx requires.
func newReq(t *testing.T, method, target string, body io.Reader) *http.Request {
	t.Helper()
	return httptest.NewRequestWithContext(context.Background(), method, target, body)
}

// byName returns the captured metrics called name.
func byName(ms []models.Metric, name string) []*models.Metric {
	var out []*models.Metric
	for i := range ms {
		if ms[i].Name == name {
			out = append(out, &ms[i])
		}
	}
	return out
}

// only returns the single metric called name and fails when there is not
// exactly one.
func only(t *testing.T, ms []models.Metric, name string) *models.Metric {
	t.Helper()
	got := byName(ms, name)
	if len(got) != 1 {
		t.Fatalf("metric %q: got %d, want 1 (all: %v)", name, len(got), ms)
	}
	return got[0]
}

// attr returns the value of attribute key of m.
func attr(m *models.Metric, key string) (attribute.Value, bool) {
	for _, kv := range m.Attributes {
		if string(kv.Key) == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

func wantString(t *testing.T, m *models.Metric, key, want string) {
	t.Helper()
	v, ok := attr(m, key)
	if !ok {
		t.Fatalf("%s: attribute %q missing (attrs %v)", m.Name, key, m.Attributes)
	}
	if got := v.String(); got != want {
		t.Fatalf("%s: %s = %q, want %q", m.Name, key, got, want)
	}
}

func wantAbsent(t *testing.T, m *models.Metric, key string) {
	t.Helper()
	if v, ok := attr(m, key); ok {
		t.Fatalf("%s: attribute %q = %q, want it absent", m.Name, key, v.String())
	}
}

func okHandler(status int, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	})
}
