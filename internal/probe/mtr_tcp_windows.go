//go:build windows

package probe

import (
	"fmt"
	"syscall"
)

// On Windows the net package binds the socket itself right before ConnectEx
// (a bind from Dialer.Control would make that second bind fail with
// WSAEINVAL), so the engine chooses the source port up front and passes it
// as the dialer's LocalAddr; Control only sets the TTL. See nextProbePort.
const tcpProbePicksPort = true

// prepareTCPProbeSocket sets the TTL on a not-yet-connected TCP socket. It
// returns port 0: the caller already knows the port it asked the dialer to
// bind.
func prepareTCPProbeSocket(fd uintptr, v6 bool, ttl int) (uint16, error) {
	var err error
	if v6 {
		err = syscall.SetsockoptInt(syscall.Handle(fd), syscall.IPPROTO_IPV6, syscall.IPV6_UNICAST_HOPS, ttl)
	} else {
		err = syscall.SetsockoptInt(syscall.Handle(fd), syscall.IPPROTO_IP, syscall.IP_TTL, ttl)
	}
	if err != nil {
		return 0, fmt.Errorf("set ttl: %w", err)
	}
	return 0, nil
}
