package netstats

import (
	"context"
	"net"

	"github.com/convoy-road-trips-app/stats"
)

// Handler serves a network connection. It matches the handler shape used by
// servers that accept connections themselves.
type Handler interface {
	// ServeConn serves conn. ctx may carry source_zone and target_zone tags,
	// see stats.ContextWithTags.
	ServeConn(ctx context.Context, conn net.Conn)
}

// NewHandler returns a Handler that instruments each connection as by NewConn,
// recording to the package default recorder, then passes it to h. The zones
// come from the options, or from the context tags source_zone and target_zone.
//
// h owns the connection and must close it; closing is what flushes the
// connection's totals.
func NewHandler(h Handler, opts ...Option) Handler {
	return &handler{h: h, sink: sink{useDefault: true}, cfg: newConfig(opts)}
}

// NewHandlerWith is like NewHandler but records to r. A nil r records nothing.
func NewHandlerWith(r stats.Recorder, h Handler, opts ...Option) Handler {
	return &handler{h: h, sink: sink{r: r}, cfg: newConfig(opts)}
}

// handler wraps connections before delegating to h.
type handler struct {
	h    Handler
	sink sink
	cfg  config
}

// ServeConn wraps conn and calls the wrapped handler with it.
func (h *handler) ServeConn(ctx context.Context, conn net.Conn) {
	h.h.ServeConn(ctx, newConn(ctx, h.sink, conn, h.cfg))
}
