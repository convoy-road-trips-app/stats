package netstats_test

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/convoy-road-trips-app/stats/models"
	"github.com/convoy-road-trips-app/stats/netstats"
	"github.com/convoy-road-trips-app/stats/statstest"
)

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
