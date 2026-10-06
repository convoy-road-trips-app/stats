package netstats

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/convoy-road-trips-app/stats"
)

// Metric names, kept from segmentio/stats.
const (
	metricOpen       = "conn.open.count"
	metricClose      = "conn.close.count"
	metricReadCount  = "conn.read.count"
	metricWriteCount = "conn.write.count"
	metricReadBytes  = "conn.read.bytes"
	metricWriteBytes = "conn.write.bytes"
	metricError      = "conn.error.count"
)

// Tag keys.
const (
	tagProtocol   = "protocol"
	tagOperation  = "operation"
	tagSourceZone = "source_zone"
	tagTargetZone = "target_zone"
	tagInZone     = "in_zone"
)

// Operation tag values.
const (
	opRead   = "read"
	opWrite  = "write"
	opClose  = "close"
	opAccept = "accept"

	opSetDeadline      = "set-deadline"
	opSetReadDeadline  = "set-read-deadline"
	opSetWriteDeadline = "set-write-deadline"
)

// noZone is the zone value used when none is configured.
const noZone = "N/A"

// unknownProtocol is the protocol tag value when the connection reports no
// local address.
const unknownProtocol = "unknown"

// defaultFlushInterval is how often an open connection flushes its read and
// write totals.
const defaultFlushInterval = 10 * time.Second

// defaultRecorder holds the recorder used by NewConn, NewListener and
// NewHandler. It is nil until SetDefaultRecorder is called.
var defaultRecorder atomic.Pointer[recorderBox]

// recorderBox lets atomic.Pointer hold an interface value.
type recorderBox struct{ r stats.Recorder }

// SetDefaultRecorder sets the recorder that NewConn, NewListener and
// NewHandler record to. Until it is called, or after it is called with nil,
// those functions record nothing. The recorder is looked up each time a metric
// is recorded, so connections created earlier pick up a later change.
//
// It is safe for concurrent use.
func SetDefaultRecorder(r stats.Recorder) {
	if r == nil {
		defaultRecorder.Store(nil)
		return
	}
	defaultRecorder.Store(&recorderBox{r: r})
}

// sink resolves the recorder to record to.
type sink struct {
	// r is the explicit recorder; it is used when useDefault is false.
	r stats.Recorder
	// useDefault selects the package default recorder.
	useDefault bool
}

// recorder returns the recorder to use, or nil when nothing should be recorded.
func (s sink) recorder() stats.Recorder {
	if s.useDefault {
		if b := defaultRecorder.Load(); b != nil {
			return b.r
		}
		return nil
	}
	return s.r
}

// Option configures NewConn, NewListener, NewHandler and their With variants.
type Option func(*config)

// config holds the resolved options.
type config struct {
	sourceZone    string
	targetZone    string
	flushInterval time.Duration
	// discover enables address-based zone discovery for unset zones.
	discover bool
}

// WithZones sets the source_zone and target_zone tags. An empty value leaves
// that zone at its default, or at the value from the context for a Handler.
//
// Zones become metric series dimensions, so use a small fixed set of names
// such as availability zones.
func WithZones(source, target string) Option {
	return func(c *config) {
		c.sourceZone = source
		c.targetZone = target
	}
}

// WithFlushInterval sets how often an open connection flushes its read and
// write totals. The default is 10 seconds. Values of zero or less are ignored.
func WithFlushInterval(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.flushInterval = d
		}
	}
}

// WithZoneDiscovery turns address-based zone discovery on or off. It is on by
// default: a zone left unset by WithZones and the context tags is taken from
// the connection's addresses, as "loopback", "link-local", "private" or
// "public" (see the package documentation). Turn it off to get "N/A" for
// unset zones.
func WithZoneDiscovery(enabled bool) Option {
	return func(c *config) { c.discover = enabled }
}

// newConfig applies opts over the defaults.
func newConfig(opts []Option) config {
	cfg := config{flushInterval: defaultFlushInterval, discover: true}
	for _, o := range opts {
		if o != nil {
			o(&cfg)
		}
	}
	return cfg
}

// zones resolves the source and target zone without discovery: an explicit
// option wins, then the context tags, then "N/A".
func (c config) zones(ctx context.Context) (source, target string) {
	return normalizeZones(c.explicitZones(ctx))
}

// explicitZones returns the zones set by option or context tag; an unset zone
// is empty.
func (c config) explicitZones(ctx context.Context) (source, target string) {
	source, target = c.sourceZone, c.targetZone
	if source != "" && target != "" {
		return source, target
	}
	for _, kv := range stats.ContextTags(ctx) {
		switch string(kv.Key) {
		case tagSourceZone:
			if source == "" {
				source = kv.Value.AsString()
			}
		case tagTargetZone:
			if target == "" {
				target = kv.Value.AsString()
			}
		}
	}
	return source, target
}

// normalizeZones replaces an unset zone with "N/A".
func normalizeZones(source, target string) (outSource, outTarget string) {
	if source == "" {
		source = noZone
	}
	if target == "" {
		target = noZone
	}
	return source, target
}

// inZone reports whether the connection stays inside one zone: both zones are
// set and equal.
func inZone(source, target string) bool {
	return source != noZone && target != noZone && source == target
}

// boolString formats b as "true" or "false".
func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
