package probe

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"

	"probe-platform/internal/protocol"
)

// MTR is a native traceroute-with-statistics implementation: several rounds
// of probes with increasing TTL. Three probe flavours share one engine:
//
//   - icmp: echo requests; hops answer Time Exceeded, the destination answers
//     Echo Reply. Matched by ICMP id/seq.
//   - udp:  one datagram per probe from its own socket to Port (default 33434);
//     hops answer Time Exceeded quoting our source port, the destination
//     answers Port Unreachable (or a datagram) — reached.
//   - tcp:  one connect() per probe with the socket's TTL set; hops answer
//     Time Exceeded quoting our source port, the destination completes or
//     refuses the handshake — reached. Looks like real traffic to firewalls
//     that drop ICMP/UDP.
//
// All flavours need a raw ICMP socket (root or CAP_NET_RAW) to see the
// Time Exceeded replies. IPv4 and IPv6 are both supported.
//
// progress receives "resolved" (the IP string) and then a "hops" event with a
// full []protocol.MTRHop snapshot after every round.
func MTR(ctx context.Context, target string, p protocol.Params, progress ProgressFunc) (*protocol.MTRResult, error) {
	maxHops := clamp(def(p.MaxHops, 30), 1, 64)
	rounds := clamp(def(p.Count, 10), 1, 100)
	timeout := time.Duration(def(p.TimeoutMs, 1000)) * time.Millisecond
	interval := time.Duration(def(p.IntervalMs, 100)) * time.Millisecond
	const sendGap = 15 * time.Millisecond // be gentle with ICMP rate limits on routers

	mode := strings.ToLower(strings.TrimSpace(p.Protocol))
	if mode == "" {
		mode = "icmp"
	}
	port := p.Port
	switch mode {
	case "icmp":
		port = 0
	case "tcp":
		port = def(port, 80)
	case "udp":
		port = def(port, 33434)
	default:
		return nil, fmt.Errorf("unsupported mtr protocol %q (icmp, tcp, udp)", p.Protocol)
	}

	dst, err := Resolve(ctx, target, p.IPVersion)
	if err != nil {
		return nil, err
	}
	v6 := dst.To4() == nil
	progress.emit("resolved", dst.String())

	conn, err := listenRawICMP(v6)
	if err != nil {
		return nil, fmt.Errorf("mtr needs a raw ICMP socket (%s): %w", PrivilegeHint(), err)
	}
	defer conn.Close()

	eng := &mtrEngine{
		ctx: ctx, mode: mode, v6: v6, dst: dst, port: port, timeout: timeout,
		icmpConn: conn, id: uint16(rand.IntN(0xfffe) + 1),
		pend:    &pendingMap{m: map[uint16]pendingProbe{}},
		replies: make(chan mtrReply, 4096),
	}
	defer eng.pend.clear()
	go eng.reader()

	hops := make([]*hopAcc, maxHops+1)
	for i := range hops {
		hops[i] = &hopAcc{ttl: i}
	}
	reachedTTL := 0
	var seq uint16
	roundsRun := 0

	for round := 0; round < rounds && ctx.Err() == nil; round++ {
		limit := maxHops
		if reachedTTL > 0 {
			limit = reachedTTL
		}
		outstanding := 0
		for ttl := 1; ttl <= limit; ttl++ {
			seq++
			if err := eng.send(ttl, seq); err != nil {
				if errors.Is(err, errUnsupported) {
					return nil, err
				}
				continue
			}
			hops[ttl].sent++
			outstanding++
			for drained := true; drained; {
				select {
				case r := <-eng.replies:
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
			case r := <-eng.replies:
				applyReply(hops, r, dst, &reachedTTL)
				outstanding--
			case <-deadline.C:
				break wait
			case <-ctx.Done():
				break wait
			}
		}
		deadline.Stop()
		eng.pend.clear() // anything still pending is lost; sockets closed
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
		Target:   target,
		IP:       dst.String(),
		Protocol: mode,
		Port:     port,
		Hops:     snapshotHops(hops, end, reachedTTL),
		Reached:  reachedTTL > 0,
		Rounds:   roundsRun,
	}
	if p.Resolve {
		reverseResolve(ctx, res.Hops)
	}
	return res, nil
}

var errUnsupported = errors.New("unsupported on this platform")

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
					cache[ip] = strings.TrimSuffix(names[0], ".")
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

// --- engine -----------------------------------------------------------------

type pendingProbe struct {
	ttl    int
	sentAt time.Time
	cancel func() // closes the per-probe socket (tcp/udp); nil for icmp
}

type pendingMap struct {
	mu sync.Mutex
	m  map[uint16]pendingProbe
}

func (p *pendingMap) add(key uint16, pr pendingProbe) {
	p.mu.Lock()
	p.m[key] = pr
	p.mu.Unlock()
}

func (p *pendingMap) take(key uint16) (pendingProbe, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pr, ok := p.m[key]
	if ok {
		delete(p.m, key)
	}
	return pr, ok
}

func (p *pendingMap) clear() {
	p.mu.Lock()
	old := p.m
	p.m = map[uint16]pendingProbe{}
	p.mu.Unlock()
	for _, pr := range old {
		if pr.cancel != nil {
			pr.cancel()
		}
	}
}

type mtrReply struct {
	ttl     int
	from    net.IP
	rtt     time.Duration
	reached bool
}

type mtrEngine struct {
	ctx      context.Context
	mode     string // icmp, tcp, udp
	v6       bool
	dst      net.IP
	port     int
	timeout  time.Duration
	icmpConn *icmp.PacketConn
	id       uint16
	pend     *pendingMap
	replies  chan mtrReply
	sendMu   sync.Mutex

	usedPorts map[uint16]bool // tcp source ports handed out so far (tcpProbePicksPort platforms)
}

// send launches one probe for ttl. The pending entry is keyed by the ICMP
// sequence (icmp) or by the probe socket's source port (tcp/udp).
func (e *mtrEngine) send(ttl int, seq uint16) error {
	switch e.mode {
	case "icmp":
		return e.sendICMP(ttl, seq)
	case "udp":
		return e.sendUDP(ttl)
	case "tcp":
		return e.sendTCP(ttl)
	}
	return errUnsupported
}

func (e *mtrEngine) sendICMP(ttl int, seq uint16) error {
	payload := make([]byte, 32)
	for i := range payload {
		payload[i] = byte(i)
	}
	b, err := echoMessage(e.v6, e.id, seq, payload)
	if err != nil {
		return err
	}
	e.sendMu.Lock()
	defer e.sendMu.Unlock()
	if err := setTTL(e.icmpConn, e.v6, ttl); err != nil {
		return fmt.Errorf("set ttl: %w", err)
	}
	e.pend.add(seq, pendingProbe{ttl: ttl, sentAt: time.Now()})
	if _, err := e.icmpConn.WriteTo(b, &net.IPAddr{IP: e.dst}); err != nil {
		e.pend.take(seq)
		return err
	}
	return nil
}

func (e *mtrEngine) sendUDP(ttl int) error {
	network, laddr := "udp4", &net.UDPAddr{IP: net.IPv4zero}
	if e.v6 {
		network, laddr = "udp6", &net.UDPAddr{IP: net.IPv6unspecified}
	}
	conn, err := net.ListenUDP(network, laddr)
	if err != nil {
		return err
	}
	if e.v6 {
		err = ipv6.NewPacketConn(conn).SetHopLimit(ttl)
	} else {
		err = ipv4.NewPacketConn(conn).SetTTL(ttl)
	}
	if err != nil {
		conn.Close()
		return fmt.Errorf("set ttl: %w", err)
	}
	lport := uint16(conn.LocalAddr().(*net.UDPAddr).Port)
	e.pend.add(lport, pendingProbe{ttl: ttl, sentAt: time.Now(), cancel: func() { conn.Close() }})
	if _, err := conn.WriteTo([]byte("probe-platform mtr"), &net.UDPAddr{IP: e.dst, Port: e.port}); err != nil {
		e.pend.take(lport)
		conn.Close()
		return err
	}
	// A datagram back from the destination (e.g. a DNS server answering) means reached.
	go func() {
		buf := make([]byte, 1500)
		_ = conn.SetReadDeadline(time.Now().Add(e.timeout))
		_, addr, err := conn.ReadFrom(buf)
		if err != nil {
			return
		}
		if ua, ok := addr.(*net.UDPAddr); ok && ua.IP.Equal(e.dst) {
			if pr, ok := e.pend.take(lport); ok {
				e.replies <- mtrReply{ttl: pr.ttl, from: e.dst, rtt: time.Since(pr.sentAt), reached: true}
			}
		}
	}()
	return nil
}

func (e *mtrEngine) sendTCP(ttl int) error {
	ctx, cancel := context.WithTimeout(e.ctx, e.timeout)
	var lport uint16
	var sentAt time.Time
	registered := make(chan struct{})
	d := net.Dialer{Timeout: e.timeout}
	if tcpProbePicksPort {
		// Windows: the kernel binds only inside connect(), so choose the
		// source port here and let the dialer bind it; the pending entry can
		// then be keyed before the SYN leaves.
		lport = e.nextProbePort()
		d.LocalAddr = &net.TCPAddr{Port: int(lport)}
	}
	var ctlErr error
	d.Control = func(network, address string, c syscall.RawConn) error {
		// Set the TTL (and, where the platform allows it, bind) first so we
		// know our source port before the SYN leaves; hops quote that port in
		// Time Exceeded.
		return c.Control(func(fd uintptr) {
			port, err := prepareTCPProbeSocket(fd, e.v6, ttl)
			if err != nil {
				ctlErr = err
				return
			}
			if port != 0 {
				lport = port
			}
			sentAt = time.Now()
			e.pend.add(lport, pendingProbe{ttl: ttl, sentAt: sentAt, cancel: cancel})
			close(registered)
		})
	}
	go func() {
		defer cancel()
		conn, err := d.DialContext(ctx, tcpNetwork(e.v6), net.JoinHostPort(e.dst.String(), fmt.Sprint(e.port)))
		if conn != nil {
			conn.Close()
		}
		select {
		case <-registered:
		default:
			return // control never ran or failed
		}
		if tcpProbePicksPort && isAddrInUse(err) {
			e.pend.take(lport) // something else holds our chosen port; this probe counts as lost
			return
		}
		// Completed handshake or an RST from the destination both mean it was reached.
		if err == nil || isConnRefused(err) {
			if pr, ok := e.pend.take(lport); ok {
				e.replies <- mtrReply{ttl: pr.ttl, from: e.dst, rtt: time.Since(pr.sentAt), reached: true}
			}
		}
	}()
	// Give Control a moment to run so a socket-level failure surfaces as an error.
	select {
	case <-registered:
		return nil
	case <-time.After(200 * time.Millisecond):
		if ctlErr != nil {
			cancel()
			return ctlErr
		}
		return nil
	}
}

// Source ports for tcp probes on platforms where the engine must choose them
// (tcpProbePicksPort). 30000-48999 sits below the dynamic range every OS uses
// for its own ephemeral sockets (Windows: 49152+), so the only collisions are
// with something already listening there; a port is never reused within one
// run so a late reply cannot be attributed to a newer probe.
const (
	probePortBase = 30000
	probePortSpan = 19000
)

// nextProbePort is called from the single sending goroutine only.
func (e *mtrEngine) nextProbePort() uint16 {
	if e.usedPorts == nil {
		e.usedPorts = map[uint16]bool{}
	}
	for i := 0; i < 1000; i++ {
		p := uint16(probePortBase + rand.IntN(probePortSpan))
		if !e.usedPorts[p] {
			e.usedPorts[p] = true
			return p
		}
	}
	e.usedPorts = map[uint16]bool{} // a run needs at most 64 hops x 100 rounds; start over rather than spin
	return e.nextProbePort()
}

func isAddrInUse(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "address already in use") || // POSIX EADDRINUSE
		strings.Contains(s, "Only one usage of each socket address") // Windows WSAEADDRINUSE
}

func tcpNetwork(v6 bool) string {
	if v6 {
		return "tcp6"
	}
	return "tcp4"
}

func isConnRefused(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "connection refused") || strings.Contains(s, "connection reset")
}

// reader turns raw ICMP packets into replies for pending probes.
func (e *mtrEngine) reader() {
	buf := make([]byte, 1500)
	proto := 1
	if e.v6 {
		proto = 58
	}
	for {
		n, peer, err := e.icmpConn.ReadFrom(buf)
		if err != nil {
			return // socket closed
		}
		now := time.Now()
		msg, err := icmp.ParseMessage(proto, buf[:n])
		if err != nil {
			continue
		}
		var (
			key    uint16
			ok     bool
			isEcho bool
		)
		switch body := msg.Body.(type) {
		case *icmp.Echo:
			if e.mode != "icmp" {
				continue
			}
			isReply := (!e.v6 && msg.Type == ipv4.ICMPTypeEchoReply) || (e.v6 && msg.Type == ipv6.ICMPTypeEchoReply)
			if !isReply || uint16(body.ID) != e.id {
				continue
			}
			key, ok, isEcho = uint16(body.Seq), true, true
		case *icmp.TimeExceeded:
			key, ok = innerKey(body.Data, e.v6, e.mode, e.id)
		case *icmp.DstUnreach:
			key, ok = innerKey(body.Data, e.v6, e.mode, e.id)
		default:
			continue
		}
		if !ok {
			continue
		}
		pr, found := e.pend.take(key)
		if !found {
			continue
		}
		if pr.cancel != nil {
			pr.cancel()
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
		e.replies <- mtrReply{ttl: pr.ttl, from: from, rtt: now.Sub(pr.sentAt), reached: (isEcho || e.mode != "icmp") && from.Equal(e.dst)}
	}
}

// innerKey extracts the probe key from the quoted original datagram inside a
// Time Exceeded / Destination Unreachable message: the ICMP sequence for echo
// probes, or the transport source port for udp/tcp probes.
func innerKey(data []byte, v6 bool, mode string, id uint16) (uint16, bool) {
	var (
		proto     byte
		transport []byte
	)
	if v6 {
		if len(data) < 40+8 {
			return 0, false
		}
		proto, transport = data[6], data[40:]
	} else {
		if len(data) < 20 {
			return 0, false
		}
		hl := int(data[0]&0x0f) * 4
		if hl < 20 || len(data) < hl+8 {
			return 0, false
		}
		proto, transport = data[9], data[hl:]
	}
	switch mode {
	case "icmp":
		if (!v6 && proto != 1) || (v6 && proto != 58) {
			return 0, false
		}
		if (!v6 && transport[0] != 8) || (v6 && transport[0] != 128) { // echo request
			return 0, false
		}
		if binary.BigEndian.Uint16(transport[4:6]) != id {
			return 0, false
		}
		return binary.BigEndian.Uint16(transport[6:8]), true
	case "udp":
		if proto != 17 {
			return 0, false
		}
		return binary.BigEndian.Uint16(transport[0:2]), true
	case "tcp":
		if proto != 6 {
			return 0, false
		}
		return binary.BigEndian.Uint16(transport[0:2]), true
	}
	return 0, false
}

// --- raw socket plumbing ----------------------------------------------------

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
