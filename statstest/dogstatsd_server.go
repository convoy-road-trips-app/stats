package statstest

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
)

// maxDatagram is the receive buffer size, the largest possible UDP payload.
const maxDatagram = 64 * 1024

// DogStatsDServer receives DogStatsD datagrams, parses them and passes every
// metric and event to a DogStatsDHandler. It mirrors the server in the
// segmentio datadog package, for tests that want to assert on what a client
// really put on the wire.
//
// The zero value is ready to use. Most tests want NewDogStatsDServer.
//
// Lines that do not parse are skipped silently: the handler is not called and
// nothing panics.
type DogStatsDServer struct{}

// ListenAndServe listens on addr and serves until the listener fails. addr is
// "host:port" or "udp://host:port" for UDP, or "unixgram:///abs/path" for a
// Unix datagram socket (not available on Windows). It returns a listen
// failure or the error that stopped Serve.
//
// ListenAndServe cannot be stopped from outside because it owns the
// connection; to control the lifetime, listen yourself and call Serve.
func (DogStatsDServer) ListenAndServe(addr string, h DogStatsDHandler) error {
	network, address, err := splitDogStatsDAddr(addr)
	if err != nil {
		return err
	}
	var lc net.ListenConfig
	conn, err := lc.ListenPacket(context.Background(), network, address)
	if err != nil {
		return fmt.Errorf("statstest: listen %s %s: %w", network, address, err)
	}
	defer func() { _ = conn.Close() }()
	return DogStatsDServer{}.Serve(conn, h)
}

// Serve reads datagrams from conn until it is closed, calling h for each
// metric and event. It returns nil when conn is closed (net.ErrClosed) and the
// read error otherwise. It does not close conn.
func (DogStatsDServer) Serve(conn net.PacketConn, h DogStatsDHandler) error {
	if conn == nil || h == nil {
		return errors.New("statstest: Serve needs a connection and a handler")
	}
	buf := make([]byte, maxDatagram)
	for {
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("statstest: read datagram: %w", err)
		}
		dispatchDatagram(h, buf[:n], from)
	}
}

// ListenAndServeDogStatsD is DogStatsDServer.ListenAndServe as a function.
func ListenAndServeDogStatsD(addr string, h DogStatsDHandler) error {
	return DogStatsDServer{}.ListenAndServe(addr, h)
}

// ServeDogStatsD is DogStatsDServer.Serve as a function.
func ServeDogStatsD(conn net.PacketConn, h DogStatsDHandler) error {
	return DogStatsDServer{}.Serve(conn, h)
}

// NewDogStatsDServer starts a DogStatsDServer on a free UDP port of 127.0.0.1
// and returns its "127.0.0.1:port" address, which can be used as
// stats.DatadogConfig.Endpoint. The server stops, and its goroutine is waited
// for, in t.Cleanup.
//
// The handler runs on the server goroutine, so it must guard any state the
// test also reads with a lock. Datagrams arrive asynchronously: wait for them
// with require.Eventually or a channel.
func NewDogStatsDServer(t testing.TB, h DogStatsDHandler) (addr string) {
	t.Helper()

	var lc net.ListenConfig
	conn, err := lc.ListenPacket(context.Background(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("statstest: listen: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- DogStatsDServer{}.Serve(conn, h) }()
	t.Cleanup(func() {
		_ = conn.Close()
		if err := <-done; err != nil {
			t.Errorf("statstest: dogstatsd server: %v", err)
		}
	})
	return conn.LocalAddr().String()
}

// splitDogStatsDAddr maps a DogStatsD address to a network and address.
func splitDogStatsDAddr(addr string) (network, address string, err error) {
	switch {
	case strings.HasPrefix(addr, "unixgram://"):
		path := strings.TrimPrefix(addr, "unixgram://")
		if path == "" {
			return "", "", fmt.Errorf("statstest: unixgram address %q has no path", addr)
		}
		return "unixgram", path, nil
	case strings.HasPrefix(addr, "udp://"):
		return "udp", strings.TrimPrefix(addr, "udp://"), nil
	case strings.Contains(addr, "://"):
		return "", "", fmt.Errorf("statstest: unsupported address scheme in %q", addr)
	default:
		return "udp", addr, nil
	}
}
