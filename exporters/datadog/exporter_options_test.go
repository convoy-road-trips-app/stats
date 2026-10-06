package datadog

import (
	"context"
	"testing"

	"github.com/convoy-road-trips-app/stats/models"
	"go.opentelemetry.io/otel/attribute"
)

func exportHistogram(t *testing.T, cfg *models.DatadogConfig, name string) {
	t.Helper()
	e, err := NewExporter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Shutdown(context.Background())
	m := &models.Metric{Name: name, Type: models.MetricTypeHistogram, Value: 2}
	if err := e.Export(context.Background(), []*models.Metric{m}); err != nil {
		t.Fatal(err)
	}
}

func TestExporterUseDistributions(t *testing.T) {
	ln := listenUDP(t)
	cfg := &models.DatadogConfig{Enabled: true, Endpoint: ln.LocalAddr().String(), UseDistributions: true}
	exportHistogram(t, cfg, "latency")
	if got := readPackets(t, ln); len(got) != 1 || got[0] != "latency:2|d" {
		t.Fatalf("got %q", got)
	}
}

func TestExporterDistributionPrefixes(t *testing.T) {
	ln := listenUDP(t)
	cfg := &models.DatadogConfig{Enabled: true, Endpoint: ln.LocalAddr().String(), DistributionPrefixes: []string{"http."}}
	exportHistogram(t, cfg, "http.latency")
	exportHistogram(t, cfg, "db.latency")
	got := readPackets(t, ln)
	if len(got) != 2 || got[0] != "http.latency:2|d" || got[1] != "db.latency:2|h" {
		t.Fatalf("got %q", got)
	}
}

func TestNewExporterCopiesDistributionPrefixes(t *testing.T) {
	ln := listenUDP(t)
	cfg := &models.DatadogConfig{Enabled: true, Endpoint: ln.LocalAddr().String(), DistributionPrefixes: []string{"http."}}
	e, err := NewExporter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Shutdown(context.Background())
	cfg.DistributionPrefixes[0] = "db."

	ms := []*models.Metric{
		{Name: "http.latency", Type: models.MetricTypeHistogram, Value: 2},
		{Name: "db.latency", Type: models.MetricTypeHistogram, Value: 2},
	}
	if err := e.Export(context.Background(), ms); err != nil {
		t.Fatal(err)
	}
	if got := readPackets(t, ln); len(got) != 1 || got[0] != "http.latency:2|d\ndb.latency:2|h" {
		t.Fatalf("got %q", got)
	}
}

func pathMetric() *models.Metric {
	return &models.Metric{
		Name: "req", Type: models.MetricTypeCounter, Value: 1,
		Attributes: []attribute.KeyValue{
			attribute.String("http_req_path", "/a/1"),
			attribute.String("method", "GET"),
		},
	}
}

func exportOne(t *testing.T, cfg *models.DatadogConfig, m *models.Metric) {
	t.Helper()
	e, err := NewExporter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Shutdown(context.Background())
	if err := e.Export(context.Background(), []*models.Metric{m}); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultFilterDropsHTTPReqPath(t *testing.T) {
	ln := listenUDP(t)
	m := pathMetric()
	exportOne(t, &models.DatadogConfig{Enabled: true, Endpoint: ln.LocalAddr().String()}, m)
	if got := readPackets(t, ln); len(got) != 1 || got[0] != "req:1|c|#method:GET" {
		t.Fatalf("got %q", got)
	}
	if len(m.Attributes) != 2 || string(m.Attributes[0].Key) != "http_req_path" {
		t.Fatalf("shared metric mutated: %v", m.Attributes)
	}
}

func TestEmptyFiltersKeepAll(t *testing.T) {
	ln := listenUDP(t)
	cfg := &models.DatadogConfig{Enabled: true, Endpoint: ln.LocalAddr().String(), Filters: []string{}}
	exportOne(t, cfg, pathMetric())
	if got := readPackets(t, ln); len(got) != 1 || got[0] != "req:1|c|#http_req_path:/a/1,method:GET" {
		t.Fatalf("got %q", got)
	}
}

func TestCustomFiltersStripOnlyListed(t *testing.T) {
	ln := listenUDP(t)
	cfg := &models.DatadogConfig{Enabled: true, Endpoint: ln.LocalAddr().String(), Filters: []string{"method"}}
	exportOne(t, cfg, pathMetric())
	if got := readPackets(t, ln); len(got) != 1 || got[0] != "req:1|c|#http_req_path:/a/1" {
		t.Fatalf("got %q", got)
	}
}

func TestNewExporterCopiesFilters(t *testing.T) {
	ln := listenUDP(t)
	cfg := &models.DatadogConfig{Enabled: true, Endpoint: ln.LocalAddr().String(), Filters: []string{"method"}}
	e, err := NewExporter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Shutdown(context.Background())
	cfg.Filters[0] = "http_req_path"

	if err := e.Export(context.Background(), []*models.Metric{pathMetric()}); err != nil {
		t.Fatal(err)
	}
	if got := readPackets(t, ln); len(got) != 1 || got[0] != "req:1|c|#http_req_path:/a/1" {
		t.Fatalf("got %q", got)
	}
}
