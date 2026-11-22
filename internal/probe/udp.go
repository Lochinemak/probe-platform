package probe

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"probe-platform/internal/protocol"
)

const maxUDPPayload = 1024

// UDP sends one datagram per attempt to target ("host:port") and reports what
// came back. UDP has no handshake, so the three outcomes mean less than a TCP
// connect does: a reply proves the service is there; an ICMP port-unreachable
// proves nothing listens; silence is either a firewall or a service that does
// not answer this particular payload.
//
// The payload is a DNS query when the port is 53 (or payload_mode=dns), which
// any DNS server answers, and a short text otherwise. With no_wait the probe
// only checks that the datagram can be sent.
//
// progress receives "resolved" and one "reply" per attempt.
func UDP(ctx context.Context, target string, p protocol.Params, progress ProgressFunc) (*protocol.PingResult, error) {
	count := clamp(def(p.Count, 3), 1, 100)
	interval := time.Duration(def(p.IntervalMs, 500)) * time.Millisecond
	timeout := time.Duration(clamp(def(p.TimeoutMs, 2000), 200, 10000)) * time.Millisecond

	host, port, err := SplitTarget(target, p.Port)
	if err != nil {
		return nil, errors.New("udp needs a port: host:port")
	}
	payload, err := udpPayload(p, port)
	if err != nil {
		return nil, err
	}
	ip, err := ResolveTarget(ctx, host, p)
	if err != nil {
		return nil, err
	}
	progress.emit("resolved", ip.String())
	addr := net.JoinHostPort(ip.String(), strconv.Itoa(port))
	res := &protocol.PingResult{Target: target, IP: ip.String(), Port: port, Proto: "udp"}
	var rtts []float64

	for i := 0; i < count; i++ {
		if i > 0 {
			if err := sleepCtx(ctx, interval); err != nil {
				break
			}
		}
		r := udpOnce(ctx, addr, payload, timeout, p)
		r.Seq = i
		if r.OK {
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

func udpOnce(ctx context.Context, addr string, payload []byte, timeout time.Duration, p protocol.Params) protocol.Reply {
	// A fresh socket per attempt: a new source port, and an ICMP error for
	// one attempt cannot be delivered to the next.
	d := net.Dialer{Timeout: timeout, Control: publicAddrControl(p.PublicOnly)}
	conn, err := d.DialContext(ctx, dialNetwork("udp", p.IPVersion), addr)
	if err != nil {
		return protocol.Reply{Error: ErrString(err)}
	}
	defer conn.Close()
	// Stop waiting as soon as the task is cancelled.
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()

	start := time.Now()
	_ = conn.SetDeadline(start.Add(timeout))
	if _, err := conn.Write(payload); err != nil {
		if isPortUnreachable(err) {
			return protocol.Reply{State: "closed", Error: "port unreachable"}
		}
		return protocol.Reply{Error: ErrString(err)}
	}
	if p.NoWait {
		return protocol.Reply{OK: true, State: "sent", RTTMs: ms(time.Since(start)), Size: len(payload)}
	}
	buf := make([]byte, 2048)
	n, err := conn.Read(buf)
	rtt := ms(time.Since(start))
	switch {
	case err == nil:
		return protocol.Reply{OK: true, State: "reply", RTTMs: rtt, Size: n}
	case isPortUnreachable(err):
		return protocol.Reply{State: "closed", Error: "port unreachable"}
	case ctx.Err() != nil:
		return protocol.Reply{Error: "cancelled"}
	case errors.Is(err, os.ErrDeadlineExceeded):
		return protocol.Reply{State: "no_reply", Error: "no reply"}
	}
	return protocol.Reply{Error: ErrString(err)}
}

// isPortUnreachable reports an ICMP port-unreachable surfacing on a connected
// UDP socket: ECONNREFUSED on Unix, WSAECONNRESET on Windows.
func isPortUnreachable(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET)
}

func udpPayload(p protocol.Params, port int) ([]byte, error) {
	mode := strings.ToLower(strings.TrimSpace(p.PayloadMode))
	if mode == "" || mode == "auto" {
		mode = "text"
		if port == 53 {
			mode = "dns"
		}
	}
	switch mode {
	case "dns":
		name := strings.TrimSuffix(strings.TrimSpace(p.QueryName), ".")
		if name == "" {
			name = "example.com"
		}
		qname, err := dnsmessage.NewName(name + ".")
		if err != nil {
			return nil, errors.New("invalid query_name")
		}
		b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: uint16(time.Now().UnixNano()), RecursionDesired: true})
		if err := b.StartQuestions(); err != nil {
			return nil, err
		}
		if err := b.Question(dnsmessage.Question{Name: qname, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}); err != nil {
			return nil, err
		}
		return b.Finish()
	case "text":
		s := p.Payload
		if s == "" {
			s = "probe"
		}
		if len(s) > maxUDPPayload {
			return nil, errors.New("payload too long (max 1024 bytes)")
		}
		return []byte(s), nil
	}
	return nil, errors.New("payload_mode must be auto, dns or text")
}
