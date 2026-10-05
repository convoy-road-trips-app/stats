package netstats_test

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats"
	"github.com/convoy-road-trips-app/stats/models"
	"github.com/convoy-road-trips-app/stats/netstats"
	"github.com/convoy-road-trips-app/stats/statstest"
)

// longFlush keeps the periodic flush out of tests that assert on Close.
const longFlush = time.Hour

// tcpPair returns a connected TCP client and server connection.
func tcpPair(t *testing.T) (client, server *net.TCPConn) {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	type result struct {
		c   net.Conn
		err error
	}
	ch := make(chan result, 1)
	go func() {
		c, err := ln.Accept()
		ch <- result{c, err}
	}()

	var d net.Dialer
	cc, err := d.DialContext(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	r := <-ch
	if r.err != nil {
		t.Fatalf("accept: %v", r.err)
	}
	t.Cleanup(func() { _ = cc.Close(); _ = r.c.Close() })
	return cc.(*net.TCPConn), r.c.(*net.TCPConn)
}

// collect flushes the client and returns the captured metrics.
func collect(t *testing.T, c *stats.Client, exp *statstest.Exporter) []models.Metric {
	t.Helper()
	statstest.Flush(t, c)
	return exp.Metrics()
}

// sum adds up the values of the metrics called name and counts the points.
func sum(ms []models.Metric, name string) (total float64, n int) {
	for i := range ms {
		if ms[i].Name == name {
			total += ms[i].Value
			n++
		}
	}
	return total, n
}

// find returns the metrics called name whose attributes include key=value.
func find(ms []models.Metric, name, key, value string) []models.Metric {
	var out []models.Metric
	for i := range ms {
		if ms[i].Name == name && attr(&ms[i], key) == value {
			out = append(out, ms[i])
		}
	}
	return out
}

// attr returns the value of attribute key on m, or "" when absent.
func attr(m *models.Metric, key string) string {
	for _, kv := range m.Attributes {
		if string(kv.Key) == key {
			return kv.Value.AsString()
		}
	}
	return ""
}

func TestConnOpenClose(t *testing.T) {
	client, exp := statstest.NewClient(t)
	cc, sc := net.Pipe()
	defer func() { _ = sc.Close() }()

	nc := netstats.NewConnWith(client, cc, netstats.WithFlushInterval(longFlush))
	ms := collect(t, client, exp)
	if v, _ := sum(ms, "conn.open.count"); v != 1 {
		t.Fatalf("open count = %v, want 1", v)
	}
	if _, n := sum(ms, "conn.close.count"); n != 0 {
		t.Fatalf("close recorded before Close")
	}

	if err := nc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	ms = collect(t, client, exp)
	if v, _ := sum(ms, "conn.close.count"); v != 1 {
		t.Fatalf("close count = %v, want 1", v)
	}

	open := find(ms, "conn.open.count", "protocol", "pipe")
	if len(open) != 1 {
		t.Fatalf("open metrics with protocol=pipe = %d, want 1", len(open))
	}
	for key, want := range map[string]string{
		"source_zone": "N/A", "target_zone": "N/A", "in_zone": "false",
	} {
		if got := attr(&open[0], key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}

	// A second Close records no second close.
	_ = nc.Close()
	if v, _ := sum(collect(t, client, exp), "conn.close.count"); v != 1 {
		t.Fatalf("close count after second Close = %v, want 1", v)
	}
}

func TestReadWriteBytesFlushedOnClose(t *testing.T) {
	client, exp := statstest.NewClient(t)
	cc, sc := tcpPair(t)
	nc := netstats.NewConnWith(client, cc, netstats.WithFlushInterval(longFlush))

	for range 3 {
		if _, err := nc.Write([]byte("hello")); err != nil { // 3 writes, 15 bytes
			t.Fatalf("write: %v", err)
		}
	}
	buf := make([]byte, 64)
	if _, err := io.ReadFull(sc, buf[:15]); err != nil {
		t.Fatalf("server read: %v", err)
	}
	if _, err := sc.Write([]byte("pong")); err != nil { // 1 read, 4 bytes
		t.Fatalf("server write: %v", err)
	}
	if n, err := nc.Read(buf); err != nil || n != 4 {
		t.Fatalf("read = %d, %v; want 4, nil", n, err)
	}

	ms := collect(t, client, exp)
	for _, name := range []string{"conn.read.count", "conn.write.count", "conn.read.bytes", "conn.write.bytes"} {
		if _, n := sum(ms, name); n != 0 {
			t.Fatalf("%s recorded before flush", name)
		}
	}

	if err := nc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	ms = collect(t, client, exp)
	for name, want := range map[string]float64{
		"conn.read.count":  1,
		"conn.write.count": 3,
		"conn.read.bytes":  4,
		"conn.write.bytes": 15,
	} {
		got, n := sum(ms, name)
		if n != 1 || got != want {
			t.Errorf("%s = %v in %d points, want %v in 1", name, got, n, want)
		}
	}
	for _, m := range ms {
		switch m.Name {
		case "conn.read.bytes", "conn.write.bytes":
			if m.Type != models.MetricTypeHistogram {
				t.Errorf("%s type = %v, want histogram", m.Name, m.Type)
			}
		case "conn.read.count", "conn.write.count":
			if m.Type != models.MetricTypeCounter {
				t.Errorf("%s type = %v, want counter", m.Name, m.Type)
			}
		}
	}
}

func TestPeriodicFlushStopsAfterClose(t *testing.T) {
	client, exp := statstest.NewClient(t)
	cc, sc := tcpPair(t)
	nc := netstats.NewConnWith(client, cc, netstats.WithFlushInterval(20*time.Millisecond))

	if _, err := nc.Write([]byte("x")); err != nil {
		t.Fatalf("write: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if v, _ := sum(collect(t, client, exp), "conn.write.count"); v == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("periodic flush never recorded conn.write.count")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := nc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	_ = sc.Close()
	// Nothing was written since the last flush, so the total stays 1 and no
	// timer fires after Close.
	time.Sleep(100 * time.Millisecond)
	ms := collect(t, client, exp)
	if v, n := sum(ms, "conn.write.count"); v != 1 || n != 1 {
		t.Fatalf("conn.write.count = %v in %d points, want 1 in 1", v, n)
	}
}

func TestEOFNotError(t *testing.T) {
	client, exp := statstest.NewClient(t)
	cc, sc := tcpPair(t)
	nc := netstats.NewConnWith(client, cc, netstats.WithFlushInterval(longFlush))

	_ = sc.Close()
	if _, err := nc.Read(make([]byte, 8)); !errors.Is(err, io.EOF) {
		t.Fatalf("read err = %v, want EOF", err)
	}
	_ = nc.Close()

	ms := collect(t, client, exp)
	if v, n := sum(ms, "conn.error.count"); n != 0 {
		t.Fatalf("conn.error.count = %v, want none for EOF", v)
	}
	if v, _ := sum(ms, "conn.read.count"); v != 1 {
		t.Fatalf("conn.read.count = %v, want 1", v)
	}
}

func TestWriteToClosedConnRecordsError(t *testing.T) {
	client, exp := statstest.NewClient(t)
	cc, _ := tcpPair(t)
	nc := netstats.NewConnWith(client, cc, netstats.WithFlushInterval(longFlush))

	if err := nc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := nc.Write([]byte("x")); err == nil {
		t.Fatal("write to closed conn succeeded")
	}

	errs := find(collect(t, client, exp), "conn.error.count", "operation", "write")
	if len(errs) != 1 || errs[0].Value != 1 {
		t.Fatalf("conn.error.count{operation=write} = %+v, want one point of 1", errs)
	}
	if got := attr(&errs[0], "protocol"); got != "tcp" {
		t.Errorf("protocol = %q, want tcp", got)
	}
}

// failingListener returns err from every Accept.
type failingListener struct {
	net.Listener
	err error
}

func (l failingListener) Accept() (net.Conn, error) { return nil, l.err }

func (l failingListener) Addr() net.Addr { return &net.TCPAddr{} }

func TestListenerAcceptErrorOperation(t *testing.T) {
	client, exp := statstest.NewClient(t)
	boom := errors.New("boom")
	ln := netstats.NewListenerWith(client, failingListener{err: boom})

	if _, err := ln.Accept(); !errors.Is(err, boom) {
		t.Fatalf("accept err = %v, want boom", err)
	}

	errs := find(collect(t, client, exp), "conn.error.count", "operation", "accept")
	if len(errs) != 1 || errs[0].Value != 1 {
		t.Fatalf("conn.error.count{operation=accept} = %+v, want one point of 1", errs)
	}
	if got := attr(&errs[0], "protocol"); got != "tcp" {
		t.Errorf("protocol = %q, want tcp", got)
	}
}

func TestListenerWrapsAcceptedConns(t *testing.T) {
	client, exp := statstest.NewClient(t)
	var lc net.ListenConfig
	raw, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ln := netstats.NewListenerWith(client, raw, netstats.WithZones("a", "a"))
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		var d net.Dialer
		c, err := d.DialContext(context.Background(), "tcp", ln.Addr().String())
		if err == nil {
			_ = c.Close()
		}
	}()
	c, err := ln.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	_ = c.Close()

	ms := collect(t, client, exp)
	open := find(ms, "conn.open.count", "in_zone", "true")
	if len(open) != 1 {
		t.Fatalf("open metrics with in_zone=true = %d, want 1", len(open))
	}
	if v, _ := sum(ms, "conn.close.count"); v != 1 {
		t.Fatalf("close count = %v, want 1", v)
	}
}

func TestCloseWritePreserved(t *testing.T) {
	client, _ := statstest.NewClient(t)
	cc, sc := tcpPair(t)
	nc := netstats.NewConnWith(client, cc, netstats.WithFlushInterval(longFlush))
	defer func() { _ = nc.Close() }()

	cw, ok := nc.(interface{ CloseWrite() error })
	if !ok {
		t.Fatal("wrapped TCP conn lost CloseWrite")
	}
	if _, ok := nc.(interface{ CloseRead() error }); !ok {
		t.Fatal("wrapped TCP conn lost CloseRead")
	}
	if err := cw.CloseWrite(); err != nil {
		t.Fatalf("CloseWrite: %v", err)
	}
	if err := sc.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("deadline: %v", err)
	}
	if _, err := sc.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("peer read err = %v, want EOF after CloseWrite", err)
	}
	if err := nc.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}
}

func TestNoCloseWriteWhenUnsupported(t *testing.T) {
	cc, sc := net.Pipe()
	defer func() { _ = sc.Close() }()
	nc := netstats.NewConnWith(nil, cc)
	defer func() { _ = nc.Close() }()
	if _, ok := nc.(interface{ CloseWrite() error }); ok {
		t.Fatal("pipe conn gained CloseWrite")
	}
}

func TestDefaultRecorder(t *testing.T) {
	// Unset: records nothing and does not panic.
	netstats.SetDefaultRecorder(nil)
	cc, sc := net.Pipe()
	defer func() { _ = sc.Close() }()
	nc := netstats.NewConn(cc)

	client, exp := statstest.NewClient(t)
	netstats.SetDefaultRecorder(client)
	t.Cleanup(func() { netstats.SetDefaultRecorder(nil) })

	// The default is looked up per record, so the close is captured.
	_ = nc.Close()
	ms := collect(t, client, exp)
	if v, _ := sum(ms, "conn.close.count"); v != 1 {
		t.Fatalf("close count = %v, want 1", v)
	}
	if _, n := sum(ms, "conn.open.count"); n != 0 {
		t.Fatalf("open recorded before default recorder was set")
	}
}

// captureHandler hands the wrapped connection to the test.
type captureHandler struct {
	wg   sync.WaitGroup
	conn net.Conn
}

func (h *captureHandler) ServeConn(_ context.Context, c net.Conn) {
	h.conn = c
	h.wg.Done()
}

func TestHandlerZonesFromContext(t *testing.T) {
	client, exp := statstest.NewClient(t)
	cc, sc := net.Pipe()
	defer func() { _ = sc.Close() }()

	inner := &captureHandler{}
	inner.wg.Add(1)
	h := netstats.NewHandlerWith(client, inner)
	ctx := stats.ContextWithTags(context.Background(),
		attribute.String("source_zone", "us-east-1a"),
		attribute.String("target_zone", "us-east-1a"),
	)
	h.ServeConn(ctx, cc)
	inner.wg.Wait()
	_ = inner.conn.Close()

	open := find(collect(t, client, exp), "conn.open.count", "source_zone", "us-east-1a")
	if len(open) != 1 {
		t.Fatalf("open metrics with source_zone from context = %d, want 1", len(open))
	}
	if got := attr(&open[0], "in_zone"); got != "true" {
		t.Errorf("in_zone = %q, want true", got)
	}
}

func TestHandlerOptionZonesWinOverContext(t *testing.T) {
	client, exp := statstest.NewClient(t)
	cc, sc := net.Pipe()
	defer func() { _ = sc.Close() }()

	inner := &captureHandler{}
	inner.wg.Add(1)
	h := netstats.NewHandlerWith(client, inner, netstats.WithZones("a", "b"))
	ctx := stats.ContextWithTags(context.Background(), attribute.String("source_zone", "z"))
	h.ServeConn(ctx, cc)
	inner.wg.Wait()
	_ = inner.conn.Close()

	open := find(collect(t, client, exp), "conn.open.count", "source_zone", "a")
	if len(open) != 1 {
		t.Fatalf("open metrics with source_zone=a = %d, want 1", len(open))
	}
	if got := attr(&open[0], "in_zone"); got != "false" {
		t.Errorf("in_zone = %q, want false", got)
	}
}
