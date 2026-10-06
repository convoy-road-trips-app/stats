package netstats

import (
	"net"
	"net/netip"
	"testing"
)

type strAddr string

func (a strAddr) Network() string { return "x" }
func (a strAddr) String() string  { return string(a) }

func TestClassifyAddr(t *testing.T) {
	tests := []struct {
		name string
		addr net.Addr
		want string
	}{
		{"nil", nil, noZone},
		{"tcp loopback v4", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1}, zoneLoopback},
		{"tcp loopback v6", &net.TCPAddr{IP: net.ParseIP("::1"), Port: 1}, zoneLoopback},
		{"udp 10/8", &net.UDPAddr{IP: net.ParseIP("10.1.2.3"), Port: 1}, zonePrivate},
		{"172.16/12", &net.TCPAddr{IP: net.ParseIP("172.31.0.9")}, zonePrivate},
		{"172.32 is public", &net.TCPAddr{IP: net.ParseIP("172.32.0.9")}, zonePublic},
		{"192.168/16", &net.IPAddr{IP: net.ParseIP("192.168.1.1")}, zonePrivate},
		{"cgnat", &net.TCPAddr{IP: net.ParseIP("100.64.0.1")}, zonePrivate},
		{"ula v6", &net.TCPAddr{IP: net.ParseIP("fd00::1")}, zonePrivate},
		{"link-local v4", &net.TCPAddr{IP: net.ParseIP("169.254.169.254")}, zoneLinkLocal},
		{"link-local v6 with zone", &net.TCPAddr{IP: net.ParseIP("fe80::1"), Zone: "en0"}, zoneLinkLocal},
		{"public v4", &net.TCPAddr{IP: net.ParseIP("8.8.8.8"), Port: 53}, zonePublic},
		{"public v6", &net.TCPAddr{IP: net.ParseIP("2001:4860:4860::8888")}, zonePublic},
		{"v4-mapped private", &net.TCPAddr{IP: net.ParseIP("::ffff:10.0.0.1")}, zonePrivate},
		{"unspecified", &net.TCPAddr{IP: net.IPv4zero}, noZone},
		{"nil ip", &net.TCPAddr{}, noZone},
		{"string addr port", strAddr("10.0.0.1:80"), zonePrivate},
		{"string addr bare", strAddr("8.8.4.4"), zonePublic},
		{"string v6 bracket", strAddr("[::1]:80"), zoneLoopback},
		{"pipe", strAddr("pipe"), noZone},
		{"unix path", &net.UnixAddr{Name: "/tmp/s", Net: "unix"}, noZone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyAddr(tt.addr); got != tt.want {
				t.Errorf("classifyAddr(%v) = %q, want %q", tt.addr, got, tt.want)
			}
		})
	}
}

func TestClassifyIP(t *testing.T) {
	if got := classifyIP(netip.Addr{}); got != noZone {
		t.Errorf("zero Addr = %q, want %q", got, noZone)
	}
}
