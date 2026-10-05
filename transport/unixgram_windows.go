//go:build windows

package transport

import "fmt"

// dialUnixgram is unavailable on Windows.
func dialUnixgram(path string) (Conn, error) {
	return nil, fmt.Errorf("%w: %q (unixgram is not supported on windows)", ErrUnsupportedNetwork, "unixgram")
}
