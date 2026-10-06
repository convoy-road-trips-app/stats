package datadog

import (
	"net"
	"testing"
	"time"

	"github.com/convoy-road-trips-app/stats/models"
)

func counter(name string, v float64) *models.Metric {
	return &models.Metric{Name: name, Type: models.MetricTypeCounter, Value: v}
}

// readPackets reads datagrams from conn until it is quiet for 300ms.
func readPackets(t *testing.T, conn net.PacketConn) []string {
	t.Helper()
	var out []string
	buf := make([]byte, 70000)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			return out
		}
		out = append(out, string(buf[:n]))
	}
}

func listenUDP(t *testing.T) *net.UDPConn {
	t.Helper()
	ln, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}
