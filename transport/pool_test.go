package transport

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

var _ Conn = (*net.UDPConn)(nil)

func TestNewPoolUDPDelivers(t *testing.T) {
	for _, network := range []string{"udp", "udp4"} {
		t.Run(network, func(t *testing.T) {
			ln, err := (&net.ListenConfig{}).ListenPacket(context.Background(), network, "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()

			pool, err := NewPool(network, ln.LocalAddr().String(), 2, time.Second)
			if err != nil {
				t.Fatalf("NewPool: %v", err)
			}
			defer pool.Close()

			if err := pool.Send(context.Background(), []byte("hello")); err != nil {
				t.Fatalf("Send: %v", err)
			}

			buf := make([]byte, 64)
			_ = ln.SetReadDeadline(time.Now().Add(2 * time.Second))
			n, _, err := ln.ReadFrom(buf)
			if err != nil {
				t.Fatalf("ReadFrom: %v", err)
			}
			if got := string(buf[:n]); got != "hello" {
				t.Fatalf("got %q, want %q", got, "hello")
			}
		})
	}
}

func TestNewPoolUnsupportedNetwork(t *testing.T) {
	for _, network := range []string{"tcp", ""} {
		pool, err := NewPool(network, "127.0.0.1:8125", 1, time.Second)
		if err == nil {
			pool.Close()
			t.Fatalf("network %q: expected error", network)
		}
		if !errors.Is(err, ErrUnsupportedNetwork) {
			t.Fatalf("network %q: got %v, want ErrUnsupportedNetwork", network, err)
		}
	}
}

func TestNewUDPConnPoolWrapper(t *testing.T) {
	ln, err := (&net.ListenConfig{}).ListenPacket(context.Background(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	pool, err := NewUDPConnPool(ln.LocalAddr().String(), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Send(context.Background(), []byte("x")); err != nil {
		t.Fatal(err)
	}
}
