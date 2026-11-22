//go:build windows

package probe

import (
	"errors"
	"net"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SystemNameservers lists the DNS servers of the network adapters that are
// up, IPv4 first. Windows has no resolv.conf; this is what `ipconfig /all`
// shows.
func SystemNameservers() ([]string, error) {
	const flags = windows.GAA_FLAG_SKIP_ANYCAST | windows.GAA_FLAG_SKIP_MULTICAST | windows.GAA_FLAG_SKIP_FRIENDLY_NAME
	size := uint32(15 * 1024)
	var buf []byte
	for i := 0; i < 4; i++ {
		buf = make([]byte, size)
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, flags, 0, (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])), &size)
		if err == nil {
			break
		}
		if err != windows.ERROR_BUFFER_OVERFLOW {
			return nil, errors.New("cannot read the adapters' DNS servers; specify dns_server")
		}
		buf = nil
	}
	if buf == nil {
		return nil, errors.New("cannot read the adapters' DNS servers; specify dns_server")
	}
	var v4, v6 []string
	seen := map[string]bool{}
	for aa := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])); aa != nil; aa = aa.Next {
		if aa.OperStatus != windows.IfOperStatusUp || aa.IfType == windows.IF_TYPE_SOFTWARE_LOOPBACK {
			continue
		}
		for d := aa.FirstDnsServerAddress; d != nil; d = d.Next {
			ip := d.Address.IP()
			// fec0:0:0:ffff::1-3 are placeholders Windows lists on adapters
			// that were given no IPv6 DNS server.
			if ip == nil || ip.IsUnspecified() || (len(ip) == net.IPv6len && ip[0] == 0xfe && ip[1] == 0xc0) {
				continue
			}
			s := ip.String()
			if seen[s] {
				continue
			}
			seen[s] = true
			if ip.To4() != nil {
				v4 = append(v4, s)
			} else if !ip.IsLinkLocalUnicast() { // a link-local server needs a zone to be dialled
				v6 = append(v6, s)
			}
		}
	}
	return append(v4, v6...), nil
}
