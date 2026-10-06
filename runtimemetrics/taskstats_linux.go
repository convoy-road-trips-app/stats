//go:build linux

package runtimemetrics

import (
	"errors"
	"fmt"
	"math"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	// replyTimeout bounds each receive (SO_RCVTIMEO) and the wait for a reply.
	replyTimeout = time.Second
	// replyBufSize holds the controller and taskstats replies, each under 1 KiB.
	replyBufSize = 8192
)

// requestSeq numbers requests so a reply to one request is never mistaken
// for the reply to another.
var requestSeq atomic.Uint32

// Get returns the cumulative delay totals of the process (thread group) pid
// from the kernel's taskstats generic netlink interface. The kernel answers
// only callers with CAP_NET_ADMIN; other callers get an error wrapping
// syscall.EPERM. IsUnsupported reports true for the error when the kernel
// has no taskstats. Get waits about a second for each reply, and each call
// uses its own socket, so it is safe for concurrent use.
func Get(pid int) (DelayInfo, error) {
	if pid <= 0 || pid > math.MaxInt32 {
		return DelayInfo{}, fmt.Errorf("taskstats: invalid pid %d", pid)
	}

	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, syscall.NETLINK_GENERIC)
	if errors.Is(err, syscall.EAFNOSUPPORT) || errors.Is(err, syscall.EPROTONOSUPPORT) {
		// No generic netlink (for example under gVisor), so no taskstats.
		return DelayInfo{}, fmt.Errorf("%w: netlink socket: %w", errTaskstatsUnsupported, err)
	}
	if err != nil {
		return DelayInfo{}, fmt.Errorf("taskstats: netlink socket: %w", err)
	}
	defer func() { _ = syscall.Close(fd) }()

	timeout := syscall.NsecToTimeval(replyTimeout.Nanoseconds())
	if err := syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &timeout); err != nil {
		return DelayInfo{}, fmt.Errorf("taskstats: set receive timeout: %w", err)
	}
	// Port id 0 lets the kernel assign a unique one.
	if err := syscall.Bind(fd, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}); err != nil {
		return DelayInfo{}, fmt.Errorf("taskstats: bind: %w", err)
	}

	seq := requestSeq.Add(1)
	family, err := exchange(fd, familyRequest(seq), seq, parseFamilyReply)
	if err != nil {
		return DelayInfo{}, err
	}
	seq = requestSeq.Add(1)
	return exchange(fd, taskstatsRequest(family, seq, uint32(pid)), seq, ParseTaskstatsReply)
}

// exchange sends msg to the kernel and reads replies until parse finds the
// answer to request seq, an error reply arrives, or replyTimeout passes.
func exchange[T any](fd int, msg []byte, seq uint32, parse func(buf []byte, seq uint32) (T, bool, error)) (T, error) {
	var zero T
	if err := syscall.Sendto(fd, msg, 0, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}); err != nil {
		return zero, fmt.Errorf("taskstats: send: %w", err)
	}

	buf := make([]byte, replyBufSize)
	deadline := time.Now().Add(replyTimeout)
	for time.Now().Before(deadline) {
		n, _, err := syscall.Recvfrom(fd, buf, 0)
		switch {
		case errors.Is(err, syscall.EINTR):
			// With SO_RCVTIMEO set, signals such as the Go runtime's
			// preemption signal interrupt the receive despite SA_RESTART.
			continue
		case errors.Is(err, syscall.EAGAIN):
			return zero, fmt.Errorf("taskstats: no reply within %v: %w", replyTimeout, err)
		case err != nil:
			return zero, fmt.Errorf("taskstats: receive: %w", err)
		}
		v, found, err := parse(buf[:n], seq)
		if err != nil || found {
			return v, err
		}
	}
	return zero, fmt.Errorf("taskstats: no reply within %v", replyTimeout)
}
