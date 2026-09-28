package probe

import (
	"net"

	"golang.org/x/net/icmp"
)

// DetectCapabilities probes the local environment and reports what this agent
// can do. Values: tcp, http, icmp (any ping), icmp_raw (raw socket), mtr, ipv6.
func DetectCapabilities() []string {
	caps := []string{"tcp", "http"}
	raw := false
	if c, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0"); err == nil {
		c.Close()
		raw = true
	}
	if raw {
		caps = append(caps, "icmp", "icmp_raw", "mtr")
	} else if c, err := icmp.ListenPacket("udp4", "0.0.0.0"); err == nil {
		c.Close()
		caps = append(caps, "icmp")
	}
	if hasGlobalIPv6() {
		caps = append(caps, "ipv6")
	}
	return caps
}

func hasGlobalIPv6() bool {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.To4() != nil {
			continue
		}
		if ipn.IP.IsGlobalUnicast() && !ipn.IP.IsPrivate() {
			return true
		}
	}
	return false
}
