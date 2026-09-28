package probe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	probing "github.com/prometheus-community/pro-bing"

	"probe-platform/internal/protocol"
)

// Ping runs an ICMP echo probe against target. It first tries a raw socket
// (CAP_NET_RAW / root) and falls back to an unprivileged ICMP datagram socket
// (Linux: net.ipv4.ping_group_range must cover the agent's gid).
//
// progress receives "resolved" (the IP string) and then one "reply"
// (protocol.Reply) per sequence number, including ones that timed out, in the
// order they are resolved.
func Ping(ctx context.Context, target string, p protocol.Params, progress ProgressFunc) (*protocol.PingResult, error) {
	count := clamp(def(p.Count, 10), 1, 100)
	interval := time.Duration(def(p.IntervalMs, 500)) * time.Millisecond
	perProbe := time.Duration(def(p.TimeoutMs, 2000)) * time.Millisecond
	size := clamp(def(p.PacketSize, 56), 24, 1400) // pro-bing needs >= 24 bytes

	ip, err := Resolve(ctx, target, p.IPVersion)
	if err != nil {
		return nil, err
	}
	progress.emit("resolved", ip.String())
	onReply := func(r protocol.Reply) { progress.emit("reply", r) }

	res, err := runPinger(ctx, target, ip, count, interval, perProbe, size, true, onReply)
	if err != nil && isPermissionError(err) {
		res, err = runPinger(ctx, target, ip, count, interval, perProbe, size, false, onReply)
		if err != nil && isPermissionError(err) {
			return nil, fmt.Errorf("icmp not permitted: run the agent as root / with CAP_NET_RAW, or set net.ipv4.ping_group_range (%v)", shortErr(err))
		}
	}
	return res, err
}

// isPermissionError is true only for failures to *open* the ICMP socket
// ("listen ip4:icmp ...: socket: operation not permitted"). A sendto that is
// refused by routing (e.g. a prohibited fake-IP route) also says "permission
// denied" but is not something a privilege change would fix.
func isPermissionError(err error) bool {
	s := strings.ToLower(err.Error())
	if strings.Contains(s, "sendto") || strings.Contains(s, "write ") {
		return false
	}
	return strings.Contains(s, "operation not permitted") ||
		strings.Contains(s, "permission denied") ||
		strings.Contains(s, "socket: protocol not supported")
}

func runPinger(ctx context.Context, target string, ip net.IP, count int, interval, perProbe time.Duration, size int, privileged bool, onReply func(protocol.Reply)) (*protocol.PingResult, error) {
	pinger := probing.New(target)
	pinger.SetIPAddr(&net.IPAddr{IP: ip})
	pinger.SetPrivileged(privileged)
	pinger.Count = count
	pinger.Interval = interval
	pinger.Size = size
	pinger.RecordRtts = false
	pinger.RecordTTLs = false
	// pro-bing only has a whole-run timeout; lost packets are detected by the
	// tracker below, so give the run enough headroom for every probe.
	pinger.Timeout = time.Duration(count)*interval + perProbe + 500*time.Millisecond

	var (
		mu      sync.Mutex
		sentAt  = map[int]time.Time{}
		replies = map[int]protocol.Reply{}
		rtts    []float64
	)
	emit := func(r protocol.Reply) {
		if onReply != nil {
			onReply(r)
		}
	}
	pinger.OnSend = func(pkt *probing.Packet) {
		mu.Lock()
		sentAt[pkt.Seq] = time.Now()
		mu.Unlock()
	}
	pinger.OnRecv = func(pkt *probing.Packet) {
		r := protocol.Reply{Seq: pkt.Seq, OK: true, RTTMs: ms(pkt.Rtt), TTL: pkt.TTL, Size: pkt.Nbytes}
		mu.Lock()
		if _, dup := replies[pkt.Seq]; dup {
			mu.Unlock()
			return
		}
		replies[pkt.Seq] = r
		delete(sentAt, pkt.Seq)
		rtts = append(rtts, r.RTTMs)
		mu.Unlock()
		emit(r)
	}
	pinger.OnRecvError = func(err error) {}

	// Tracker: mark probes older than perProbe as lost so the UI sees
	// timeouts live instead of at the very end.
	trackCtx, stopTrack := context.WithCancel(ctx)
	trackDone := make(chan struct{})
	markLost := func(final bool) {
		mu.Lock()
		var lost []int
		for seq, t := range sentAt {
			if final || time.Since(t) >= perProbe {
				lost = append(lost, seq)
			}
		}
		sort.Ints(lost)
		var out []protocol.Reply
		for _, seq := range lost {
			r := protocol.Reply{Seq: seq, OK: false, Error: "timeout"}
			replies[seq] = r
			delete(sentAt, seq)
			out = append(out, r)
		}
		mu.Unlock()
		for _, r := range out {
			emit(r)
		}
	}
	go func() {
		defer close(trackDone)
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-trackCtx.Done():
				return
			case <-t.C:
				markLost(false)
			}
		}
	}()

	// Stop early once every sequence number has been resolved instead of
	// waiting for pro-bing's whole-run timeout on lossy links.
	go func() {
		t := time.NewTicker(50 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-trackCtx.Done():
				return
			case <-t.C:
				mu.Lock()
				done := len(replies) >= count && len(sentAt) == 0
				mu.Unlock()
				if done {
					pinger.Stop()
					return
				}
			}
		}
	}()

	err := pinger.RunWithContext(ctx)
	stopTrack()
	<-trackDone
	if err != nil && !errors.Is(err, context.Canceled) {
		return nil, err
	}
	markLost(true)

	mu.Lock()
	defer mu.Unlock()
	res := &protocol.PingResult{Target: target, IP: ip.String()}
	seqs := make([]int, 0, len(replies))
	for s := range replies {
		seqs = append(seqs, s)
	}
	sort.Ints(seqs)
	for _, s := range seqs {
		res.Replies = append(res.Replies, replies[s])
	}
	sent := pinger.PacketsSent
	if sent < len(res.Replies) {
		sent = len(res.Replies)
	}
	res.Stats = ComputeStats(sent, rtts)
	return res, nil
}
