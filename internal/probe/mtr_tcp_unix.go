//go:build !windows

package probe

import (
	"fmt"
	"syscall"
)

// tcpProbePicksPort is false here: the kernel assigns the source port when
// prepareTCPProbeSocket binds, and reports it back.
const tcpProbePicksPort = false

// prepareTCPProbeSocket sets the TTL on a not-yet-connected TCP socket and
// binds it to an ephemeral port so the caller learns the source port before
// connect(). Runs inside net.Dialer.Control.
func prepareTCPProbeSocket(fd uintptr, v6 bool, ttl int) (uint16, error) {
	var err error
	if v6 {
		err = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, syscall.IPV6_UNICAST_HOPS, ttl)
	} else {
		err = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_TTL, ttl)
	}
	if err != nil {
		return 0, fmt.Errorf("set ttl: %w", err)
	}
	var sa syscall.Sockaddr = &syscall.SockaddrInet4{}
	if v6 {
		sa = &syscall.SockaddrInet6{}
	}
	if err := syscall.Bind(int(fd), sa); err != nil {
		return 0, fmt.Errorf("bind: %w", err)
	}
	local, err := syscall.Getsockname(int(fd))
	if err != nil {
		return 0, err
	}
	switch l := local.(type) {
	case *syscall.SockaddrInet4:
		return uint16(l.Port), nil
	case *syscall.SockaddrInet6:
		return uint16(l.Port), nil
	}
	return 0, fmt.Errorf("unexpected local address %T", local)
}
