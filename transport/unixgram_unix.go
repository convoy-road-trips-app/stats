//go:build !windows

package transport

import (
	"context"
	"fmt"
	"net"
)

// unixgramWriteBuffer is the requested socket send buffer. Failure to apply it
// is not fatal: kernels cap or reject large values for unix datagram sockets.
const unixgramWriteBuffer = 1024 * 1024

// dialUnixgram connects a datagram unix socket to the listener at path.
func dialUnixgram(path string) (Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(context.Background(), networkUnixgram, path)
	if err != nil {
		return nil, fmt.Errorf("dial unixgram %q: %w", path, err)
	}
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("dial unixgram %q: unexpected connection type %T", path, conn)
	}
	_ = uc.SetWriteBuffer(unixgramWriteBuffer)
	return uc, nil
}
