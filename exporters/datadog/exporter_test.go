package datadog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/convoy-road-trips-app/stats/models"
	"go.opentelemetry.io/otel/attribute"
)

func counter(name string, v float64) *models.Metric {
	return &models.Metric{Name: name, Type: models.MetricTypeCounter, Value: v}
}

// readPackets reads datagrams from conn until it is quiet for 300ms.
func readPackets(t *testing.T, conn net.PacketConn) []string {
	t.Helper()
	var out []string
	buf := make([]byte, 70000)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			return out
		}
		out = append(out, string(buf[:n]))
	}
}

func listenUDP(t *testing.T) *net.UDPConn {
	t.Helper()
	ln, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

func TestUnixgramDelivers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unixgram is unavailable on Windows")
	}
	dir, err := os.MkdirTemp("", "sp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "s.sock")

	ln, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	e, err := NewExporter(&models.DatadogConfig{Enabled: true, Endpoint: "unixgram://" + path})
	if err != nil {
		t.Fatalf("NewExporter: %v", err)
	}
	defer e.Shutdown(context.Background())

	if err := e.Export(context.Background(), []*models.Metric{counter("a.b", 1), counter("c.d", 2)}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	got := readPackets(t, ln)
	if len(got) != 1 || got[0] != "a.b:1|c\nc.d:2|c" {
		t.Fatalf("got %q, want one packet %q", got, "a.b:1|c\nc.d:2|c")
	}
}

func TestPacketNeverSplitsLine(t *testing.T) {
	ln := listenUDP(t)
	const size = 100
	e, err := NewExporter(&models.DatadogConfig{Enabled: true, Endpoint: ln.LocalAddr().String(), BufferSize: size})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Shutdown(context.Background())

	want := map[string]bool{}
	batch := make([]*models.Metric, 0, 40)
	for i := range 40 {
		// Varying lengths so packet boundaries fall in different places.
		m := counter(fmt.Sprintf("m%d.%s", i, strings.Repeat("x", i%17)), float64(i))
		batch = append(batch, m)
		want[fmt.Sprintf("%s:%d|c", m.Name, i)] = true
	}
	if err := e.Export(context.Background(), batch); err != nil {
		t.Fatalf("Export: %v", err)
	}

	packets := readPackets(t, ln)
	if len(packets) < 2 {
		t.Fatalf("expected the batch to span several packets, got %d", len(packets))
	}
	seen := 0
	for _, p := range packets {
		if len(p) > size {
			t.Fatalf("packet of %d bytes exceeds buffer size %d", len(p), size)
		}
		for line := range strings.SplitSeq(p, "\n") {
			if !want[line] {
				t.Fatalf("line %q is not a whole serialized metric", line)
			}
			delete(want, line)
			seen++
		}
	}
	if seen != len(batch) || len(want) != 0 {
		t.Fatalf("delivered %d lines, missing %d", seen, len(want))
	}
}

func TestOversizedLineDroppedAndReported(t *testing.T) {
	ln := listenUDP(t)
	e, err := NewExporter(&models.DatadogConfig{Enabled: true, Endpoint: ln.LocalAddr().String(), BufferSize: 32})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Shutdown(context.Background())

	big := counter(strings.Repeat("big", 20), 1)
	err = e.Export(context.Background(), []*models.Metric{counter("ok.one", 1), big, counter("ok.two", 2)})
	if err == nil || !strings.Contains(err.Error(), "dropped 1 line") {
		t.Fatalf("Export error = %v, want dropped-line error", err)
	}
	got := strings.Join(readPackets(t, ln), "\n")
	if got != "ok.one:1|c\nok.two:2|c" {
		t.Fatalf("delivered %q, want the two small lines", got)
	}
}

func TestOversizedLineDoesNotOpenBreaker(t *testing.T) {
	ln := listenUDP(t)
	e, err := NewExporter(&models.DatadogConfig{Enabled: true, Endpoint: ln.LocalAddr().String(), BufferSize: 32})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Shutdown(context.Background())

	big := counter(strings.Repeat("big", 20), 1)
	for range 20 {
		if err := e.Export(context.Background(), []*models.Metric{big}); err == nil {
			t.Fatal("want dropped-line error")
		}
	}
	if err := e.Export(context.Background(), []*models.Metric{counter("ok", 1)}); err != nil {
		t.Fatalf("good export after oversized ones failed: %v", err)
	}
	if got := readPackets(t, ln); len(got) != 1 || got[0] != "ok:1|c" {
		t.Fatalf("got %q", got)
	}
}

func TestPacketize(t *testing.T) {
	lines := [][]byte{[]byte("aaaa"), []byte("bbbb"), []byte("cccccccccc"), []byte("dd")}
	packets, oversized := packetize(lines, 9)
	if oversized != 1 {
		t.Fatalf("oversized = %d, want 1", oversized)
	}
	want := [][]byte{[]byte("aaaa\nbbbb"), []byte("dd")}
	if len(packets) != len(want) {
		t.Fatalf("packets = %q, want %q", packets, want)
	}
	for i := range want {
		if !bytes.Equal(packets[i], want[i]) {
			t.Fatalf("packet %d = %q, want %q", i, packets[i], want[i])
		}
	}
}

func TestNewExporterCopiesConfig(t *testing.T) {
	ln := listenUDP(t)
	cfg := &models.DatadogConfig{Enabled: true, Endpoint: ln.LocalAddr().String(), Tags: []string{"env:test"}}
	e, err := NewExporter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Shutdown(context.Background())
	cfg.Tags[0] = "env:mutated"
	cfg.Enabled = false

	if err := e.Export(context.Background(), []*models.Metric{counter("x", 1)}); err != nil {
		t.Fatal(err)
	}
	if got := readPackets(t, ln); len(got) != 1 || got[0] != "x:1|c|#env:test" {
		t.Fatalf("got %q", got)
	}
}

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

func TestSendEventDelivers(t *testing.T) {
	ln := listenUDP(t)
	exp, err := NewExporter(&models.DatadogConfig{Enabled: true, AgentHost: "127.0.0.1", AgentPort: ln.LocalAddr().(*net.UDPAddr).Port})
	if err != nil {
		t.Fatal(err)
	}
	defer exp.Shutdown(context.Background())

	if err := exp.SendEvent(context.Background(), models.DatadogEvent{Title: "t", Text: "a\nb"}); err != nil {
		t.Fatal(err)
	}
	got := readPackets(t, ln)
	if len(got) != 1 || got[0] != `_e{1,4}:t|a\nb` {
		t.Fatalf("got %q", got)
	}
}

func TestSendEventTooLarge(t *testing.T) {
	ln := listenUDP(t)
	exp, err := NewExporter(&models.DatadogConfig{
		Enabled: true, AgentHost: "127.0.0.1", AgentPort: ln.LocalAddr().(*net.UDPAddr).Port, BufferSize: 32,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer exp.Shutdown(context.Background())

	err = exp.SendEvent(context.Background(), models.DatadogEvent{Title: "t", Text: strings.Repeat("x", 64)})
	if !errors.Is(err, models.ErrEventTooLarge) {
		t.Fatalf("err = %v, want ErrEventTooLarge", err)
	}
	if got := readPackets(t, ln); len(got) != 0 {
		t.Fatalf("oversized event was sent: %q", got)
	}
}
