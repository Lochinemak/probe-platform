// Package probe implements the measurements probe agents run: ICMP ping, TCP
// ping, HTTP, MTR, DNS, UDP, TLS, a DNS comparison across resolvers, and the
// node's own status.
package probe

import (
	"context"
	"fmt"

	"probe-platform/internal/protocol"
)

// ProgressFunc receives partial results while a probe runs. kind is one of
// "reply" (ping/tcping/udp: protocol.Reply), "attempt" (http:
// protocol.HTTPAttempt, dns: protocol.DNSAttempt, tls: protocol.TLSAttempt),
// "hops" (mtr: []protocol.MTRHop) or "entry" (dns_compare:
// protocol.DNSCompareEntry). "resolved" carries the resolved IP.
type ProgressFunc func(kind string, data any)

func (f ProgressFunc) emit(kind string, data any) {
	if f != nil {
		f(kind, data)
	}
}

// Run executes task and returns its typed result payload.
func Run(ctx context.Context, task protocol.Task, progress ProgressFunc) (any, error) {
	switch task.Type {
	case protocol.TaskPing:
		return Ping(ctx, task.Target, task.Params, progress)
	case protocol.TaskTCPing:
		return TCPing(ctx, task.Target, task.Params, progress)
	case protocol.TaskHTTP:
		return HTTP(ctx, task.Target, task.Params, progress)
	case protocol.TaskMTR:
		return MTR(ctx, task.Target, task.Params, progress)
	case protocol.TaskDNS:
		return DNS(ctx, task.Target, task.Params, progress)
	case protocol.TaskUDP:
		return UDP(ctx, task.Target, task.Params, progress)
	case protocol.TaskTLS:
		return TLS(ctx, task.Target, task.Params, progress)
	case protocol.TaskDNSCompare:
		return DNSCompare(ctx, task.Target, task.Params, progress)
	case protocol.TaskStatus:
		return Status(ctx)
	default:
		return nil, fmt.Errorf("unknown task type %q", task.Type)
	}
}
