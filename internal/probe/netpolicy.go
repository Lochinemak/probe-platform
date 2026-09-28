package probe

import (
	"fmt"
	"net"
)

// Destination policy. A probe started by an anonymous guest must never be able
// to reach anything but the public internet: the agents sit inside home LANs
// and on a cloud host, so an unrestricted target turns every node into an
// internal port scanner and an SSRF proxy (cloud metadata at 169.254.169.254,
// router admin pages, NAS panels...).
//
// Params.PublicOnly carries that restriction from the server to the agent. The
// server sets it for every guest task and validates the target up front; the
// agent enforces it again on the resolved address, which is what closes DNS
// rebinding (a name that resolves publicly for the server and privately for
// the agent).

// IsPublicIP reports whether ip is a globally routable unicast address, i.e. a
// legitimate probe destination for an untrusted caller. Loopback, private,
// link-local (including the cloud metadata address), CGNAT, multicast,
// benchmarking and reserved ranges are all rejected.
func IsPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	// IsGlobalUnicast already excludes loopback, multicast, link-local and the
	// unspecified address; IsPrivate covers RFC 1918 and IPv6 fc00::/7.
	if !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		switch {
		case ip4[0] == 0: // "this network"
			return false
		case ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127: // CGNAT 100.64.0.0/10
			return false
		case ip4[0] == 192 && ip4[1] == 0 && ip4[2] == 0: // IETF protocol assignments
			return false
		case ip4[0] == 192 && ip4[1] == 0 && ip4[2] == 2: // TEST-NET-1
			return false
		case ip4[0] == 198 && (ip4[1] == 18 || ip4[1] == 19): // benchmarking / fake-IP
			return false
		case ip4[0] == 198 && ip4[1] == 51 && ip4[2] == 100: // TEST-NET-2
			return false
		case ip4[0] == 203 && ip4[1] == 0 && ip4[2] == 113: // TEST-NET-3
			return false
		case ip4[0] >= 240: // reserved, including the broadcast address
			return false
		}
		return true
	}
	switch {
	case ip[0] == 0x20 && ip[1] == 0x01 && ip[2] == 0x0d && ip[3] == 0xb8: // 2001:db8::/32 documentation
		return false
	case ip[0] == 0x01 && ip[1] == 0x00 && ip[2] == 0x00 && ip[3] == 0x00: // 100::/64 discard-only
		return false
	}
	return true
}

// PrivateTargetError explains a rejected destination to the dashboard user.
func PrivateTargetError(host string, ip net.IP) error {
	where := host
	if ip != nil && ip.String() != host {
		where = fmt.Sprintf("%s (%s)", host, ip)
	}
	return fmt.Errorf("%s 不是公网地址：未登录的访客只能探测公网目标，内网、回环与云元数据地址已被拒绝", where)
}

// CheckPublicAddr validates a "host:port" or bare-IP address string against
// IsPublicIP. Used as a net.Dialer.Control hook so every connection a probe
// makes, including the ones behind HTTP redirects, is checked after the name
// was resolved.
func CheckPublicAddr(address string) error {
	host := address
	if h, _, err := net.SplitHostPort(address); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	if !IsPublicIP(ip) {
		return PrivateTargetError(host, ip)
	}
	return nil
}
