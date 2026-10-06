package netstats

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/convoy-road-trips-app/stats"
)

// NewConn wraps c so its lifecycle, traffic and errors are recorded to the
// package default recorder (see SetDefaultRecorder). It records
// conn.open.count immediately.
//
// Read and write totals are flushed when the connection is closed and every 10
// seconds while it is in use, not on every call; see the package
// documentation. Always close the returned connection, or its flush timer
// keeps running.
//
// CloseRead and CloseWrite are available on the result when c has them, as
// *net.TCPConn does.
func NewConn(c net.Conn, opts ...Option) net.Conn {
	return newConn(context.Background(), sink{useDefault: true}, c, newConfig(opts))
}

// NewConnWith is like NewConn but records to r. A nil r records nothing.
func NewConnWith(r stats.Recorder, c net.Conn, opts ...Option) net.Conn {
	return newConn(context.Background(), sink{r: r}, c, newConfig(opts))
}

// BaseConn is implemented by the connections NewConn, NewListener and
// NewHandler return. BaseConn returns the connection that was wrapped, the
// way segmentio/stats exposes it. Assert it on a net.Conn to reach the
// underlying connection, and implement it on your own wrapper to keep the
// chain reachable:
//
//	if bc, ok := c.(netstats.BaseConn); ok {
//		tcp, _ := bc.BaseConn().(*net.TCPConn)
//	}
type BaseConn interface {
	net.Conn
	// BaseConn returns the wrapped connection.
	BaseConn() net.Conn
}

// conn is the instrumented connection. Read and write counts live in atomics
// and are drained by flush.
type conn struct {
	net.Conn
	ctx  context.Context
	sink sink
	tags []stats.MetricOption

	flushInterval time.Duration

	readCalls  atomic.Int64
	readBytes  atomic.Int64
	writeCalls atomic.Int64
	writeBytes atomic.Int64

	// armed is set once the flush timer has been started.
	armed atomic.Bool
	// closed is set when Close first runs.
	closed atomic.Bool

	mu    sync.Mutex // guards timer and the closed transition
	timer *time.Timer
}

// newConn builds the wrapper, records the open, and picks the concrete type
// that preserves the optional CloseRead and CloseWrite methods of c.
func newConn(ctx context.Context, s sink, c net.Conn, cfg config) net.Conn {
	source, target := cfg.connZones(ctx, c)
	protocol := unknownProtocol
	if a := c.LocalAddr(); a != nil {
		protocol = a.Network()
	}
	nc := &conn{
		Conn:          c,
		ctx:           context.WithoutCancel(ctx),
		sink:          s,
		flushInterval: cfg.flushInterval,
		tags: []stats.MetricOption{
			stats.WithAttribute(tagProtocol, protocol),
			stats.WithAttribute(tagSourceZone, source),
			stats.WithAttribute(tagTargetZone, target),
			stats.WithAttribute(tagInZone, boolString(inZone(source, target))),
		},
	}
	nc.count(metricOpen, 1)

	_, cr := c.(interface{ CloseRead() error })
	_, cw := c.(interface{ CloseWrite() error })
	switch {
	case cr && cw:
		return &connCloseReadWrite{nc}
	case cw:
		return &connCloseWrite{nc}
	case cr:
		return &connCloseRead{nc}
	default:
		return nc
	}
}

// count records a counter with the connection tags.
func (c *conn) count(name string, v float64, extra ...stats.MetricOption) {
	r := c.sink.recorder()
	if r == nil || v <= 0 {
		return
	}
	_ = r.Counter(c.ctx, name, v, append(extra, c.tags...)...)
}

// observe records a histogram observation with the connection tags.
func (c *conn) observe(name string, v float64) {
	r := c.sink.recorder()
	if r == nil || v <= 0 {
		return
	}
	_ = r.Histogram(c.ctx, name, v, c.tags...)
}

// fail records a conn.error.count for operation, ignoring a nil error and EOF.
func (c *conn) fail(operation string, err error) {
	if err == nil || errors.Is(err, io.EOF) {
		return
	}
	c.count(metricError, 1, stats.WithAttribute(tagOperation, operation))
}

// BaseConn returns the connection that was wrapped, so callers can reach
// methods of the concrete type, such as SyscallConn or SetKeepAlive.
func (c *conn) BaseConn() net.Conn { return c.Conn }

// SetDeadline sets the read and write deadlines. A failure is counted as
// conn.error.count{operation="set-deadline"}.
func (c *conn) SetDeadline(t time.Time) error {
	err := c.Conn.SetDeadline(t)
	c.fail(opSetDeadline, err)
	return err
}

// SetReadDeadline sets the read deadline. A failure is counted as
// conn.error.count{operation="set-read-deadline"}.
func (c *conn) SetReadDeadline(t time.Time) error {
	err := c.Conn.SetReadDeadline(t)
	c.fail(opSetReadDeadline, err)
	return err
}

// SetWriteDeadline sets the write deadline. A failure is counted as
// conn.error.count{operation="set-write-deadline"}.
func (c *conn) SetWriteDeadline(t time.Time) error {
	err := c.Conn.SetWriteDeadline(t)
	c.fail(opSetWriteDeadline, err)
	return err
}

// Read reads from the connection, counting the call and the bytes returned.
// io.EOF is not an error.
func (c *conn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	c.readCalls.Add(1)
	c.readBytes.Add(int64(n))
	c.fail(opRead, err)
	c.touch()
	return n, err
}

// Write writes to the connection, counting the call and the bytes written.
func (c *conn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	c.writeCalls.Add(1)
	c.writeBytes.Add(int64(n))
	c.fail(opWrite, err)
	c.touch()
	return n, err
}

// Close closes the connection once for metric purposes: it stops the flush
// timer, flushes the read and write totals and records conn.close.count. A
// second Close still closes the underlying connection, and reports its error,
// but records no further close.
func (c *conn) Close() error {
	err := c.Conn.Close()
	c.fail(opClose, err)

	c.mu.Lock()
	first := !c.closed.Swap(true)
	if c.timer != nil {
		c.timer.Stop()
	}
	c.mu.Unlock()

	if first {
		c.flush()
		c.count(metricClose, 1)
	}
	return err
}

// touch starts the periodic flush on first use. After Close it flushes
// straight away, so a late call is not lost.
func (c *conn) touch() {
	if c.closed.Load() {
		c.flush()
		return
	}
	if c.armed.Load() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Load() || c.armed.Load() {
		return
	}
	c.armed.Store(true)
	c.timer = time.AfterFunc(c.flushInterval, c.tick)
}

// tick is the timer callback: it flushes and re-arms unless the connection is
// closed. Holding mu orders it against Close, so Close's Stop always wins.
func (c *conn) tick() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Load() {
		return
	}
	c.flush()
	c.timer.Reset(c.flushInterval)
}

// flush drains the atomics and records the totals. Zero totals record
// nothing.
func (c *conn) flush() {
	c.count(metricReadCount, float64(c.readCalls.Swap(0)))
	c.count(metricWriteCount, float64(c.writeCalls.Swap(0)))
	c.observe(metricReadBytes, float64(c.readBytes.Swap(0)))
	c.observe(metricWriteBytes, float64(c.writeBytes.Swap(0)))
}

// connCloseRead preserves CloseRead.
type connCloseRead struct{ *conn }

// CloseRead shuts down the reading side of the connection.
func (c *connCloseRead) CloseRead() error {
	return c.Conn.(interface{ CloseRead() error }).CloseRead()
}

// connCloseWrite preserves CloseWrite.
type connCloseWrite struct{ *conn }

// CloseWrite shuts down the writing side of the connection.
func (c *connCloseWrite) CloseWrite() error {
	return c.Conn.(interface{ CloseWrite() error }).CloseWrite()
}

// connCloseReadWrite preserves CloseRead and CloseWrite, as *net.TCPConn has.
type connCloseReadWrite struct{ *conn }

// CloseRead shuts down the reading side of the connection.
func (c *connCloseReadWrite) CloseRead() error {
	return c.Conn.(interface{ CloseRead() error }).CloseRead()
}

// CloseWrite shuts down the writing side of the connection.
func (c *connCloseReadWrite) CloseWrite() error {
	return c.Conn.(interface{ CloseWrite() error }).CloseWrite()
}
