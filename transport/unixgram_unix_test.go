//go:build !windows

package transport

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var _ Conn = (*net.UnixConn)(nil)

// shortSocketPath returns a socket path short enough for macOS (~104 bytes).
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "sp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

func TestUnixgramDelivers(t *testing.T) {
	path := shortSocketPath(t)
	ln, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	pool, err := NewPool("unixgram", path, 2, time.Second)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	if err := pool.Send(context.Background(), []byte("hello")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := pool.SendBatch(context.Background(), [][]byte{[]byte("a"), []byte("b")}); err != nil {
		t.Fatalf("SendBatch: %v", err)
	}

	buf := make([]byte, 64)
	_ = ln.SetReadDeadline(time.Now().Add(2 * time.Second))
	want := []string{"hello", "a", "b"}
	for _, w := range want {
		n, _, err := ln.ReadFrom(buf)
		if err != nil {
			t.Fatalf("ReadFrom: %v", err)
		}
		if got := string(buf[:n]); got != w {
			t.Fatalf("got %q, want %q", got, w)
		}
	}
}

func TestUnixgramMissingPath(t *testing.T) {
	path := shortSocketPath(t) // never listened on
	pool, err := NewPool("unixgram", path, 1, time.Second)
	if err == nil {
		pool.Close()
		t.Fatal("expected dial error for missing socket")
	}
}
