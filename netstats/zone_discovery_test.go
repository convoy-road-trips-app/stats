package netstats_test

import (
	"context"
	"net"
	"testing"

	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats"
	"github.com/convoy-road-trips-app/stats/netstats"
	"github.com/convoy-road-trips-app/stats/statstest"
)

// fakeAddrConn is a connection with chosen addresses.
type fakeAddrConn struct {
	net.Conn
	local, remote net.Addr
}

func (c fakeAddrConn) LocalAddr() net.Addr  { return c.local }
func (c fakeAddrConn) RemoteAddr() net.Addr { return c.remote }
func (c fakeAddrConn) Close() error         { return nil }

func newFakeAddrConn(local, remote string) net.Conn {
	return fakeAddrConn{
		local:  &net.TCPAddr{IP: net.ParseIP(local), Port: 1},
		remote: &net.TCPAddr{IP: net.ParseIP(remote), Port: 2},
	}
}

func openZones(t *testing.T, c net.Conn, opts ...netstats.Option) (src, dst, in string) {
	t.Helper()
	client, exp := statstest.NewClient(t)
	_ = netstats.NewConnWith(client, c, opts...)
	open := find(collect(t, client, exp), "conn.open.count", "protocol", "tcp")
	if len(open) != 1 {
		t.Fatalf("open metrics = %d, want 1", len(open))
	}
	return attr(&open[0], "source_zone"), attr(&open[0], "target_zone"), attr(&open[0], "in_zone")
}

type markHandler struct{ served *bool }

func (h markHandler) ServeConn(context.Context, net.Conn) { *h.served = true }

func TestZoneDiscovered(t *testing.T) {
	tests := []struct {
		name          string
		local, remote string
		src, dst, in  string
	}{
		{"loopback", "127.0.0.1", "127.0.0.1", "loopback", "loopback", "true"},
		{"private to private", "10.0.0.5", "10.1.0.6", "private", "private", "true"},
		{"private to public", "10.0.0.5", "8.8.8.8", "private", "public", "false"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src, dst, in := openZones(t, newFakeAddrConn(tt.local, tt.remote))
			if src != tt.src || dst != tt.dst || in != tt.in {
				t.Errorf("zones = %q %q %q, want %q %q %q", src, dst, in, tt.src, tt.dst, tt.in)
			}
		})
	}
}

func TestZoneDiscoveryPrecedence(t *testing.T) {
	c := newFakeAddrConn("10.0.0.5", "8.8.8.8")

	// Option wins over discovery; the unset side is still discovered.
	src, dst, in := openZones(t, c, netstats.WithZones("us-east-1a", ""))
	if src != "us-east-1a" || dst != "public" || in != "false" {
		t.Errorf("option over discovery = %q %q %q", src, dst, in)
	}

	// Both options.
	src, dst, in = openZones(t, c, netstats.WithZones("z", "z"))
	if src != "z" || dst != "z" || in != "true" {
		t.Errorf("both options = %q %q %q", src, dst, in)
	}

	// Discovery disabled falls back to N/A.
	src, dst, in = openZones(t, c, netstats.WithZoneDiscovery(false))
	if src != "N/A" || dst != "N/A" || in != "false" {
		t.Errorf("disabled = %q %q %q", src, dst, in)
	}
}

func TestZoneContextWinsOverDiscovery(t *testing.T) {
	client, exp := statstest.NewClient(t)
	ctx := stats.ContextWithTags(context.Background(),
		attribute.String("source_zone", "ctx-a"), attribute.String("target_zone", "ctx-a"))
	var served bool
	h := netstats.NewHandlerWith(client, markHandler{&served})
	h.ServeConn(ctx, newFakeAddrConn("10.0.0.5", "8.8.8.8"))
	if !served {
		t.Fatal("handler not called")
	}
	open := find(collect(t, client, exp), "conn.open.count", "source_zone", "ctx-a")
	if len(open) != 1 || attr(&open[0], "target_zone") != "ctx-a" || attr(&open[0], "in_zone") != "true" {
		t.Fatalf("context zones not used over discovery: %v", open)
	}
}
