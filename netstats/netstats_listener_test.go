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
	"github.com/convoy-road-trips-app/stats/netstats"
	"github.com/convoy-road-trips-app/stats/statstest"
)

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
