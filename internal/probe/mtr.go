package probe

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"net"
	"sort"
	"sync"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"

	"probe-platform/internal/protocol"
)

// MTR is a native traceroute-with-statistics implementation (ICMP echo,
// increasing TTL, several rounds). It needs a raw ICMP socket, i.e. root or
// CAP_NET_RAW on Linux. IPv4 and IPv6 are both supported.
//
// progress receives "resolved" (the IP string) and then a "hops" event with a
// full []protocol.MTRHop snapshot after every round.
func MTR(ctx context.Context, target string, p protocol.Params, progress ProgressFunc) (*protocol.MTRResult, error) {
	maxHops := clamp(def(p.MaxHops, 30), 1, 64)
	rounds := clamp(def(p.Count, 10), 1, 100)
	timeout := time.Duration(def(p.TimeoutMs, 1000)) * time.Millisecond
	interval := time.Duration(def(p.IntervalMs, 100)) * time.Millisecond
	const sendGap = 15 * time.Millisecond // be gentle with ICMP rate limits on routers

	dst, err := Resolve(ctx, target, p.IPVersion)
	if err != nil {
		return nil, err
	}
	v6 := dst.To4() == nil
	progress.emit("resolved", dst.String())

	conn, err := listenRawICMP(v6)
	if err != nil {
		return nil, fmt.Errorf("mtr needs a raw ICMP socket (run the agent as root or grant CAP_NET_RAW): %w", err)
	}
	defer conn.Close()

	id := uint16(rand.IntN(0xfffe) + 1)
	pend := &pendingMap{m: map[uint16]pendingProbe{}}
	replies := make(chan mtrReply, 4096)
	go mtrReader(conn, v6, id, dst, pend, replies)

	hops := make([]*hopAcc, maxHops+1)
	for i := range hops {
		hops[i] = &hopAcc{ttl: i}
	}
	reachedTTL := 0
	var seq uint16
	payload := make([]byte, 32)
	for i := range payload {
		payload[i] = byte(i)
	}
	dstAddr := &net.IPAddr{IP: dst}
	roundsRun := 0

	for round := 0; round < rounds && ctx.Err() == nil; round++ {
		limit := maxHops
		if reachedTTL > 0 {
			limit = reachedTTL
		}
		outstanding := 0
		for ttl := 1; ttl <= limit; ttl++ {
			seq++
			if err := setTTL(conn, v6, ttl); err != nil {
				return nil, fmt.Errorf("set ttl: %w", err)
			}
			b, err := echoMessage(v6, id, seq, payload)
			if err != nil {
				return nil, err
			}
			pend.add(seq, pendingProbe{ttl: ttl, sentAt: time.Now()})
			hops[ttl].sent++
			if _, err := conn.WriteTo(b, dstAddr); err != nil {
				pend.take(seq)
				hops[ttl].sent--
				continue
			}
			outstanding++
			// Drain replies that already arrived so the channel never blocks
			// the reader.
			for drained := true; drained; {
				select {
				case r := <-replies:
					applyReply(hops, r, dst, &reachedTTL)
					outstanding--
				default:
					drained = false
				}
			}
			if err := sleepCtx(ctx, sendGap); err != nil {
				break
			}
		}

		deadline := time.NewTimer(timeout)
	wait:
		for outstanding > 0 {
			select {
			case r := <-replies:
				applyReply(hops, r, dst, &reachedTTL)
				outstanding--
			case <-deadline.C:
				break wait
			case <-ctx.Done():
				break wait
			}
		}
		deadline.Stop()
		pend.clear() // anything still pending is lost
		roundsRun++

		progress.emit("hops", snapshotHops(hops, effectiveEnd(hops, maxHops, reachedTTL), reachedTTL))
		if round < rounds-1 {
			if err := sleepCtx(ctx, interval); err != nil {
				break
			}
		}
	}

	end := effectiveEnd(hops, maxHops, reachedTTL)
	res := &protocol.MTRResult{
		Target:  target,
		IP:      dst.String(),
		Hops:    snapshotHops(hops, end, reachedTTL),
		Reached: reachedTTL > 0,
		Rounds:  roundsRun,
	}
	if p.Resolve {
		reverseResolve(ctx, res.Hops)
	}
	return res, nil
}

// effectiveEnd decides how many hops to report: up to the destination if it
// answered, otherwise up to the last responding hop plus a couple of silent
// ones so the reader can see where the path went dark.
func effectiveEnd(hops []*hopAcc, maxHops, reachedTTL int) int {
	if reachedTTL > 0 {
		return reachedTTL
	}
	last := 0
	for ttl := 1; ttl <= maxHops; ttl++ {
		if hops[ttl].recv > 0 {
			last = ttl
		}
	}
	if last == 0 {
		return min(maxHops, 5)
	}
	return min(maxHops, last+2)
}

type hopAcc struct {
	ttl     int
	sent    int
	recv    int
	hosts   []string
	hostSet map[string]bool
	rtts    []float64
	last    float64
	reached bool
}

func (h *hopAcc) addHost(ip string) {
	if h.hostSet == nil {
		h.hostSet = map[string]bool{}
	}
	if !h.hostSet[ip] {
		h.hostSet[ip] = true
		h.hosts = append(h.hosts, ip)
	}
}

func applyReply(hops []*hopAcc, r mtrReply, dst net.IP, reachedTTL *int) {
	if r.ttl < 1 || r.ttl >= len(hops) {
		return
	}
	h := hops[r.ttl]
	h.recv++
	h.addHost(r.from.String())
	rtt := ms(r.rtt)
	h.rtts = append(h.rtts, rtt)
	h.last = rtt
	if r.reached || r.from.Equal(dst) {
		h.reached = true
		if *reachedTTL == 0 || r.ttl < *reachedTTL {
			*reachedTTL = r.ttl
		}
	}
}

func snapshotHops(hops []*hopAcc, end, reachedTTL int) []protocol.MTRHop {
	out := make([]protocol.MTRHop, 0, end)
	for ttl := 1; ttl <= end; ttl++ {
		h := hops[ttl]
		st := ComputeStats(h.sent, h.rtts)
		hop := protocol.MTRHop{
			TTL:      ttl,
			Hosts:    append([]string(nil), h.hosts...),
			Sent:     st.Sent,
			Received: st.Received,
			LossPct:  st.LossPct,
			LastMs:   h.last,
			AvgMs:    st.AvgMs,
			BestMs:   st.MinMs,
			WorstMs:  st.MaxMs,
			StdDevMs: st.StdDevMs,
			Reached:  h.reached && ttl == reachedTTL,
		}
		if hop.Hosts == nil {
			hop.Hosts = []string{}
		}
		out = append(out, hop)
	}
	return out
}

func reverseResolve(ctx context.Context, hops []protocol.MTRHop) {
	cache := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for _, h := range hops {
		for _, ip := range h.Hosts {
			mu.Lock()
			_, seen := cache[ip]
			if !seen {
				cache[ip] = ""
			}
			mu.Unlock()
			if seen {
				continue
			}
			wg.Add(1)
			go func(ip string) {
				defer wg.Done()
				names, err := net.DefaultResolver.LookupAddr(rctx, ip)
				if err == nil && len(names) > 0 {
					mu.Lock()
					cache[ip] = trimDot(names[0])
					mu.Unlock()
				}
			}(ip)
		}
	}
	wg.Wait()
	for i := range hops {
		hops[i].Names = make([]string, len(hops[i].Hosts))
		for j, ip := range hops[i].Hosts {
			hops[i].Names[j] = cache[ip]
		}
	}
}

func trimDot(s string) string {
	if n := len(s); n > 0 && s[n-1] == '.' {
		return s[:n-1]
	}
	return s
}

// --- raw socket plumbing ----------------------------------------------------

type pendingProbe struct {
	ttl    int
	sentAt time.Time
}

type pendingMap struct {
	mu sync.Mutex
	m  map[uint16]pendingProbe
}

func (p *pendingMap) add(seq uint16, pr pendingProbe) {
	p.mu.Lock()
	p.m[seq] = pr
	p.mu.Unlock()
}

func (p *pendingMap) take(seq uint16) (pendingProbe, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pr, ok := p.m[seq]
	if ok {
		delete(p.m, seq)
	}
	return pr, ok
}

func (p *pendingMap) clear() {
	p.mu.Lock()
	p.m = map[uint16]pendingProbe{}
	p.mu.Unlock()
}

type mtrReply struct {
	ttl     int
	from    net.IP
	rtt     time.Duration
	reached bool // echo reply (as opposed to time-exceeded / unreachable)
}

func listenRawICMP(v6 bool) (*icmp.PacketConn, error) {
	if v6 {
		return icmp.ListenPacket("ip6:ipv6-icmp", "::")
	}
	return icmp.ListenPacket("ip4:icmp", "0.0.0.0")
}

func setTTL(conn *icmp.PacketConn, v6 bool, ttl int) error {
	if v6 {
		return conn.IPv6PacketConn().SetHopLimit(ttl)
	}
	return conn.IPv4PacketConn().SetTTL(ttl)
}

func echoMessage(v6 bool, id, seq uint16, payload []byte) ([]byte, error) {
	var typ icmp.Type = ipv4.ICMPTypeEcho
	if v6 {
		typ = ipv6.ICMPTypeEchoRequest
	}
	m := icmp.Message{Type: typ, Code: 0, Body: &icmp.Echo{ID: int(id), Seq: int(seq), Data: payload}}
	return m.Marshal(nil)
}

func mtrReader(conn *icmp.PacketConn, v6 bool, id uint16, dst net.IP, pend *pendingMap, out chan<- mtrReply) {
	buf := make([]byte, 1500)
	proto := 1
	if v6 {
		proto = 58
	}
	for {
		n, peer, err := conn.ReadFrom(buf)
		if err != nil {
			return // socket closed
		}
		now := time.Now()
		msg, err := icmp.ParseMessage(proto, buf[:n])
		if err != nil {
			continue
		}
		var (
			seq    uint16
			ok     bool
			isEcho bool
		)
		switch body := msg.Body.(type) {
		case *icmp.Echo:
			isReply := (!v6 && msg.Type == ipv4.ICMPTypeEchoReply) || (v6 && msg.Type == ipv6.ICMPTypeEchoReply)
			if !isReply || uint16(body.ID) != id {
				continue
			}
			seq, ok, isEcho = uint16(body.Seq), true, true
		case *icmp.TimeExceeded:
			seq, ok = innerSeq(body.Data, v6, id)
		case *icmp.DstUnreach:
			seq, ok = innerSeq(body.Data, v6, id)
		default:
			continue
		}
		if !ok {
			continue
		}
		pr, found := pend.take(seq)
		if !found {
			continue
		}
		var from net.IP
		switch a := peer.(type) {
		case *net.IPAddr:
			from = a.IP
		case *net.UDPAddr:
			from = a.IP
		default:
			continue
		}
		out <- mtrReply{ttl: pr.ttl, from: from, rtt: now.Sub(pr.sentAt), reached: isEcho && from.Equal(dst)}
	}
}

// innerSeq extracts our echo ID/sequence from the quoted original datagram in
// a Time Exceeded / Destination Unreachable message.
func innerSeq(data []byte, v6 bool, id uint16) (uint16, bool) {
	var inner []byte
	if v6 {
		if len(data) < 40+8 {
			return 0, false
		}
		inner = data[40:]
		if inner[0] != 128 { // ICMPv6 echo request
			return 0, false
		}
	} else {
		if len(data) < 20 {
			return 0, false
		}
		hl := int(data[0]&0x0f) * 4
		if hl < 20 || len(data) < hl+8 {
			return 0, false
		}
		inner = data[hl:]
		if inner[0] != 8 { // ICMP echo request
			return 0, false
		}
	}
	if binary.BigEndian.Uint16(inner[4:6]) != id {
		return 0, false
	}
	return binary.BigEndian.Uint16(inner[6:8]), true
}

// sortedHosts is a small helper used by tests.
func sortedHosts(h []string) []string {
	out := append([]string(nil), h...)
	sort.Strings(out)
	return out
}
