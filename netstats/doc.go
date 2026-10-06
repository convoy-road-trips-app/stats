// Package netstats instruments net.Conn, net.Listener and connection
// handlers with connection metrics.
//
// # Metrics
//
// The metric names are the ones segmentio/stats uses:
//
//	conn.open.count    counter    connections wrapped (opened)
//	conn.close.count   counter    connections closed
//	conn.read.count    counter    Read calls
//	conn.write.count   counter    Write calls
//	conn.read.bytes    histogram  bytes read, one observation per flush
//	conn.write.bytes   histogram  bytes written, one observation per flush
//	conn.error.count   counter    failed operations, with an operation tag
//
// Every metric carries the tags protocol (the local address network, such as
// tcp), source_zone, target_zone and in_zone. in_zone is "true" only when both
// zones are set and equal.
//
// conn.error.count adds operation, one of read, write, close, accept,
// set-deadline, set-read-deadline or set-write-deadline. A failed SetDeadline,
// SetReadDeadline or SetWriteDeadline is counted this way and its error is
// returned unchanged.
//
// The connection returned by NewConn implements BaseConn, which returns the
// wrapped connection.
//
// # Zones
//
// Each zone is resolved per side, in this order: WithZones; for a Handler, the
// source_zone and target_zone tags carried by the context (see
// stats.ContextWithTags); then address discovery; then "N/A".
//
// Address discovery names the source zone from the local address and the
// target zone from the remote address: "loopback", "link-local", "private"
// (RFC 1918, RFC 4193 and the RFC 6598 shared space 100.64.0.0/10) or
// "public". A non-IP address, such as a Unix socket or net.Pipe, stays "N/A".
// segmentio/stats instead looks up AWS availability zones with
// github.com/segmentio/vpcinfo; this package has no such dependency, so
// in_zone here means both ends are in the same class of network, not the same
// availability zone. Use WithZones for real zone names, or WithZoneDiscovery
// to turn discovery off.
//
// # Difference from segmentio/stats
//
// segmentio records a metric on every Read and Write call. Doing that here
// would flood the metric pipeline, so each connection counts its reads and
// writes in local atomics and flushes the totals as conn.read.count,
// conn.write.count and one conn.read.bytes and conn.write.bytes observation
// when the connection is closed, and otherwise every 10 seconds. Errors,
// opens and closes are still recorded as they happen. Totals are therefore
// visible with a delay of up to the flush interval, and a connection that is
// never closed keeps a timer running for as long as it is in use.
//
// # Recorder
//
// NewConn, NewListener and NewHandler record to the package default recorder,
// set with SetDefaultRecorder. Until it is set they record nothing. The
// ...With variants take the recorder explicitly.
//
// The package imports only the stats package; it never imports internal
// packages.
package netstats
