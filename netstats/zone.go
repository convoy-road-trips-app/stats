package netstats

import (
	"context"
	"net"
	"net/netip"
)

// Zone names produced by address discovery. They are a small fixed set, so
// they are safe as metric dimensions.
const (
	zoneLoopback  = "loopback"
	zoneLinkLocal = "link-local"
	zonePrivate   = "private"
	zonePublic    = "public"
)

// cgnat is the shared address space of RFC 6598, used by many cloud and
// overlay networks. netip.Addr.IsPrivate does not cover it.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// classifyAddr names the zone of a network address from the address alone:
// loopback, link-local, private (RFC 1918, RFC 4193 and RFC 6598) or public.
// It returns noZone for a nil address, a non-IP address such as a Unix socket
// or an in-memory pipe, and an unspecified address.
//
// segmentio/stats discovers availability zones from AWS metadata through
// github.com/segmentio/vpcinfo. That needs a network call and a dependency,
// so this is the offline equivalent: it tells where traffic goes, not which
// availability zone it lands in.
func classifyAddr(a net.Addr) string {
	ip, ok := addrIP(a)
	if !ok {
		return noZone
	}
	return classifyIP(ip)
}

// classifyIP applies the classification rules to ip.
func classifyIP(ip netip.Addr) string {
	ip = ip.Unmap().WithZone("")
	switch {
	case !ip.IsValid(), ip.IsUnspecified(), ip.IsMulticast():
		return noZone
	case ip.IsLoopback():
		return zoneLoopback
	case ip.IsLinkLocalUnicast():
		return zoneLinkLocal
	case ip.IsPrivate(), cgnat.Contains(ip):
		return zonePrivate
	default:
		return zonePublic
	}
}

// addrIP extracts the IP address of a.
func addrIP(a net.Addr) (netip.Addr, bool) {
	switch v := a.(type) {
	case nil:
		return netip.Addr{}, false
	case *net.TCPAddr:
		return fromIP(v.IP)
	case *net.UDPAddr:
		return fromIP(v.IP)
	case *net.IPAddr:
		return fromIP(v.IP)
	}
	s := a.String()
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr(), true
	}
	if ip, err := netip.ParseAddr(s); err == nil {
		return ip, true
	}
	return netip.Addr{}, false
}

// fromIP converts a net.IP, reporting false for a nil or malformed one.
func fromIP(ip net.IP) (netip.Addr, bool) {
	a, ok := netip.AddrFromSlice(ip)
	return a, ok
}

// connZones resolves the zones of c: an explicit option wins, then the context
// tags, then address discovery when enabled, then "N/A". The local address is
// the source and the remote address is the target.
func (cfg config) connZones(ctx context.Context, c net.Conn) (source, target string) {
	source, target = cfg.explicitZones(ctx)
	if cfg.discover {
		if source == "" {
			source = classifyAddr(c.LocalAddr())
		}
		if target == "" {
			target = classifyAddr(c.RemoteAddr())
		}
	}
	return normalizeZones(source, target)
}
