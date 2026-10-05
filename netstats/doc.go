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
// tcp), source_zone, target_zone and in_zone. A zone defaults to "N/A" and
// in_zone to "false"; in_zone is "true" only when both zones are set and equal.
// conn.error.count adds operation, one of read, write, close or accept.
//
// Zones come from WithZones, or, for a Handler, from the source_zone and
// target_zone tags carried by the context (see stats.ContextWithTags). An
// explicit WithZones value wins over the context.
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
