//go:build windows

package transport

import (
	"errors"
	"testing"
	"time"
)

func TestUnixgramUnsupportedOnWindows(t *testing.T) {
	pool, err := NewPool("unixgram", `C:\nonexistent.sock`, 1, time.Second)
	if err == nil {
		pool.Close()
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrUnsupportedNetwork) {
		t.Fatalf("got %v, want ErrUnsupportedNetwork", err)
	}
}
