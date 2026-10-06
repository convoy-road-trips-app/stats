package netstats_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/convoy-road-trips-app/stats"
	"github.com/convoy-road-trips-app/stats/models"
	"github.com/convoy-road-trips-app/stats/statstest"
)

// longFlush keeps the periodic flush out of tests that assert on Close.
const longFlush = time.Hour

// tcpPair returns a connected TCP client and server connection.
func tcpPair(t *testing.T) (client, server *net.TCPConn) {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	type result struct {
		c   net.Conn
		err error
	}
	ch := make(chan result, 1)
	go func() {
		c, err := ln.Accept()
		ch <- result{c, err}
	}()

	var d net.Dialer
	cc, err := d.DialContext(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	r := <-ch
	if r.err != nil {
		t.Fatalf("accept: %v", r.err)
	}
	t.Cleanup(func() { _ = cc.Close(); _ = r.c.Close() })
	return cc.(*net.TCPConn), r.c.(*net.TCPConn)
}

// collect flushes the client and returns the captured metrics.
func collect(t *testing.T, c *stats.Client, exp *statstest.Exporter) []models.Metric {
	t.Helper()
	statstest.Flush(t, c)
	return exp.Metrics()
}

// sum adds up the values of the metrics called name and counts the points.
func sum(ms []models.Metric, name string) (total float64, n int) {
	for i := range ms {
		if ms[i].Name == name {
			total += ms[i].Value
			n++
		}
	}
	return total, n
}

// find returns the metrics called name whose attributes include key=value.
func find(ms []models.Metric, name, key, value string) []models.Metric {
	var out []models.Metric
	for i := range ms {
		if ms[i].Name == name && attr(&ms[i], key) == value {
			out = append(out, ms[i])
		}
	}
	return out
}

// attr returns the value of attribute key on m, or "" when absent.
func attr(m *models.Metric, key string) string {
	for _, kv := range m.Attributes {
		if string(kv.Key) == key {
			return kv.Value.AsString()
		}
	}
	return ""
}
