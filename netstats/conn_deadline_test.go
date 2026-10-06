package netstats_test

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/convoy-road-trips-app/stats/netstats"
	"github.com/convoy-road-trips-app/stats/statstest"
)

// deadlineConn is a connection whose deadline setters fail with err.
type deadlineConn struct {
	net.Conn
	err error
}

func (c deadlineConn) SetDeadline(time.Time) error      { return c.err }
func (c deadlineConn) SetReadDeadline(time.Time) error  { return c.err }
func (c deadlineConn) SetWriteDeadline(time.Time) error { return c.err }
func (c deadlineConn) LocalAddr() net.Addr              { return strAddr("pipe") }
func (c deadlineConn) RemoteAddr() net.Addr             { return strAddr("pipe") }
func (c deadlineConn) Close() error                     { return nil }

type strAddr string

func (a strAddr) Network() string { return "pipe" }
func (a strAddr) String() string  { return string(a) }

func TestDeadlineErrorsCounted(t *testing.T) {
	boom := errors.New("boom")
	client, exp := statstest.NewClient(t)
	nc := netstats.NewConnWith(client, deadlineConn{err: boom}, netstats.WithFlushInterval(longFlush))

	calls := map[string]func(time.Time) error{
		"set-deadline":       nc.SetDeadline,
		"set-read-deadline":  nc.SetReadDeadline,
		"set-write-deadline": nc.SetWriteDeadline,
	}
	for op, call := range calls {
		if err := call(time.Time{}); !errors.Is(err, boom) {
			t.Fatalf("%s error = %v, want %v", op, err, boom)
		}
	}
	ms := collect(t, client, exp)
	for op := range calls {
		got := find(ms, "conn.error.count", "operation", op)
		if len(got) != 1 || got[0].Value != 1 {
			t.Errorf("conn.error.count{operation=%s} = %v, want one point of 1", op, got)
		}
	}
}

func TestDeadlineSuccessNotCounted(t *testing.T) {
	client, exp := statstest.NewClient(t)
	nc := netstats.NewConnWith(client, deadlineConn{}, netstats.WithFlushInterval(longFlush))

	for _, call := range []func(time.Time) error{nc.SetDeadline, nc.SetReadDeadline, nc.SetWriteDeadline} {
		if err := call(time.Time{}); err != nil {
			t.Fatalf("deadline error = %v", err)
		}
	}
	if _, n := sum(collect(t, client, exp), "conn.error.count"); n != 0 {
		t.Fatalf("conn.error.count points = %d, want 0", n)
	}
}

func TestBaseConn(t *testing.T) {
	client, _ := statstest.NewClient(t)
	cc, _ := tcpPair(t)
	nc := netstats.NewConnWith(client, cc)

	bc, ok := nc.(netstats.BaseConn)
	if !ok {
		t.Fatalf("%T does not implement netstats.BaseConn", nc)
	}
	if got := bc.BaseConn(); got != net.Conn(cc) {
		t.Errorf("BaseConn() = %v, want the wrapped connection", got)
	}

	// A user wrapper can satisfy BaseConn by forwarding to the wrapped value.
	var w netstats.BaseConn = userWrap{bc}
	if got := w.BaseConn(); got != net.Conn(cc) {
		t.Errorf("user wrapper BaseConn() = %v, want the wrapped connection", got)
	}
}

// userWrap is a user-defined wrapper that exposes the base connection.
type userWrap struct{ inner netstats.BaseConn }

func (w userWrap) Read(b []byte) (int, error)         { return w.inner.Read(b) }
func (w userWrap) Write(b []byte) (int, error)        { return w.inner.Write(b) }
func (w userWrap) Close() error                       { return w.inner.Close() }
func (w userWrap) LocalAddr() net.Addr                { return w.inner.LocalAddr() }
func (w userWrap) RemoteAddr() net.Addr               { return w.inner.RemoteAddr() }
func (w userWrap) SetDeadline(t time.Time) error      { return w.inner.SetDeadline(t) }
func (w userWrap) SetReadDeadline(t time.Time) error  { return w.inner.SetReadDeadline(t) }
func (w userWrap) SetWriteDeadline(t time.Time) error { return w.inner.SetWriteDeadline(t) }
func (w userWrap) BaseConn() net.Conn                 { return w.inner.BaseConn() }
