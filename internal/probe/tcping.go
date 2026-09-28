package probe

import (
	"context"
	"net"
	"strconv"
	"time"

	"probe-platform/internal/protocol"
)

// TCPing measures TCP connect latency to target ("host", "host:port" or a URL).
// progress receives "resolved" (the IP string) and one "reply" per attempt.
func TCPing(ctx context.Context, target string, p protocol.Params, progress ProgressFunc) (*protocol.PingResult, error) {
	count := clamp(def(p.Count, 10), 1, 100)
	interval := time.Duration(def(p.IntervalMs, 500)) * time.Millisecond
	timeout := time.Duration(def(p.TimeoutMs, 3000)) * time.Millisecond

	host, port, err := SplitTarget(target, def(p.Port, 80))
	if err != nil {
		return nil, err
	}
	ip, err := Resolve(ctx, host, p.IPVersion, p.PublicOnly)
	if err != nil {
		return nil, err
	}
	progress.emit("resolved", ip.String())
	addr := net.JoinHostPort(ip.String(), strconv.Itoa(port))
	res := &protocol.PingResult{Target: target, IP: ip.String(), Port: port}
	var rtts []float64

	for i := 0; i < count; i++ {
		if i > 0 {
			if err := sleepCtx(ctx, interval); err != nil {
				break
			}
		}
		d := net.Dialer{Timeout: timeout}
		start := time.Now()
		conn, err := d.DialContext(ctx, "tcp", addr)
		rtt := time.Since(start)
		r := protocol.Reply{Seq: i, OK: err == nil, RTTMs: ms(rtt)}
		if err != nil {
			r.Error = ErrString(err)
			r.RTTMs = 0
		} else {
			conn.Close()
			rtts = append(rtts, r.RTTMs)
		}
		res.Replies = append(res.Replies, r)
		progress.emit("reply", r)
		if ctx.Err() != nil {
			break
		}
	}
	res.Stats = ComputeStats(len(res.Replies), rtts)
	return res, nil
}
