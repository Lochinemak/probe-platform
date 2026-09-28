// Package probe implements the four measurement types (ICMP ping, TCP ping,
// HTTP ping, MTR) used by probe agents.
package probe

import (
	"context"
	"fmt"

	"probe-platform/internal/protocol"
)

// ProgressFunc receives partial results while a probe runs. kind is one of
// "reply" (ping/tcping: protocol.Reply), "attempt" (http: protocol.HTTPAttempt)
// or "hops" (mtr: []protocol.MTRHop). "resolved" carries the resolved IP.
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
	default:
		return nil, fmt.Errorf("unknown task type %q", task.Type)
	}
}
