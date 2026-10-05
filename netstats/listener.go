package netstats

import (
	"context"
	"net"

	"github.com/convoy-road-trips-app/stats"
)

// NewListener wraps l so every accepted connection is instrumented as by
// NewConn, recording to the package default recorder, and Accept errors are
// recorded as conn.error.count{operation="accept"}.
func NewListener(l net.Listener, opts ...Option) net.Listener {
	return &listener{Listener: l, sink: sink{useDefault: true}, cfg: newConfig(opts)}
}

// NewListenerWith is like NewListener but records to r. A nil r records
// nothing.
func NewListenerWith(r stats.Recorder, l net.Listener, opts ...Option) net.Listener {
	return &listener{Listener: l, sink: sink{r: r}, cfg: newConfig(opts)}
}

// listener instruments the connections of an embedded net.Listener.
type listener struct {
	net.Listener
	sink sink
	cfg  config
}

// Accept waits for the next connection and returns it instrumented. An error
// from the underlying listener is counted, including net.ErrClosed when the
// listener is shut down, and returned unchanged.
func (l *listener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		l.fail()
		return nil, err
	}
	return newConn(context.Background(), l.sink, c, l.cfg), nil
}

// fail records an accept error. The listener has no connection, so the zone
// tags come from the options only.
func (l *listener) fail() {
	r := l.sink.recorder()
	if r == nil {
		return
	}
	protocol := unknownProtocol
	if a := l.Addr(); a != nil {
		protocol = a.Network()
	}
	source, target := l.cfg.zones(context.Background())
	_ = r.Counter(context.Background(), metricError, 1,
		stats.WithAttribute(tagProtocol, protocol),
		stats.WithAttribute(tagOperation, opAccept),
		stats.WithAttribute(tagSourceZone, source),
		stats.WithAttribute(tagTargetZone, target),
		stats.WithAttribute(tagInZone, boolString(inZone(source, target))),
	)
}
