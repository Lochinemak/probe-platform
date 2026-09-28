package probe

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"probe-platform/internal/protocol"
)

// DNS queries a name against a chosen server (or the node's system resolver)
// and reports rcode, answers with TTLs and the round-trip time. Unlike the
// other probes it deliberately does not reject fake-IP answers: showing them
// is the point.
func DNS(ctx context.Context, target string, p protocol.Params, progress ProgressFunc) (*protocol.DNSResult, error) {
	count := clamp(def(p.Count, 1), 1, 20)
	interval := time.Duration(def(p.IntervalMs, 500)) * time.Millisecond
	timeout := time.Duration(def(p.TimeoutMs, 3000)) * time.Millisecond

	name := strings.TrimSuffix(strings.TrimSpace(target), ".")
	if name == "" || strings.ContainsAny(name, " /\\") {
		return nil, fmt.Errorf("invalid name %q", target)
	}
	rtype, rname, err := dnsType(p.RecordType)
	if err != nil {
		return nil, err
	}
	server, proto, err := dnsServer(p.DNSServer)
	if err != nil {
		return nil, err
	}
	// An untrusted caller may not point the query at an internal resolver: that
	// would make the agent a probe for anything listening on the LAN. The
	// node's own system resolver (empty spec) stays allowed.
	if p.PublicOnly && strings.TrimSpace(p.DNSServer) != "" {
		if err := checkPublicDNSServer(ctx, server, proto, p.IPVersion); err != nil {
			return nil, err
		}
	}

	res := &protocol.DNSResult{Target: name, RecordType: rname}
	var rtts []float64
	for i := 0; i < count; i++ {
		if i > 0 {
			if err := sleepCtx(ctx, interval); err != nil {
				break
			}
		}
		a := dnsQuery(ctx, name+".", rtype, server, proto, timeout, p.IPVersion, p.PublicOnly)
		a.Seq = i
		res.Attempts = append(res.Attempts, a)
		if a.OK {
			rtts = append(rtts, a.RTTMs)
		}
		progress.emit("attempt", a)
		if ctx.Err() != nil {
			break
		}
	}
	res.Stats = ComputeStats(len(res.Attempts), rtts)
	return res, nil
}

func dnsType(s string) (dnsmessage.Type, string, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if s == "" {
		s = "A"
	}
	types := map[string]dnsmessage.Type{
		"A": dnsmessage.TypeA, "AAAA": dnsmessage.TypeAAAA, "CNAME": dnsmessage.TypeCNAME, "MX": dnsmessage.TypeMX,
		"TXT": dnsmessage.TypeTXT, "NS": dnsmessage.TypeNS, "PTR": dnsmessage.TypePTR, "SOA": dnsmessage.TypeSOA, "SRV": dnsmessage.TypeSRV,
	}
	t, ok := types[s]
	if !ok {
		return 0, "", fmt.Errorf("unsupported record type %q", s)
	}
	return t, s, nil
}

// dnsServer normalises the user's server spec into (address, proto).
func dnsServer(spec string) (string, string, error) {
	spec = strings.TrimSpace(spec)
	switch {
	case spec == "":
		s, err := systemNameserver()
		return s, "udp", err
	case strings.HasPrefix(spec, "https://"):
		if _, err := url.Parse(spec); err != nil {
			return "", "", fmt.Errorf("invalid DoH url: %w", err)
		}
		return spec, "doh", nil
	case strings.HasPrefix(spec, "tcp://"):
		return withPort(strings.TrimPrefix(spec, "tcp://")), "tcp", nil
	default:
		return withPort(strings.TrimPrefix(spec, "udp://")), "udp", nil
	}
}

func withPort(hostport string) string {
	if _, _, err := net.SplitHostPort(hostport); err == nil {
		return hostport
	}
	return net.JoinHostPort(strings.Trim(hostport, "[]"), "53")
}

// systemNameserver returns the first nameserver from /etc/resolv.conf.
func systemNameserver() (string, error) {
	f, err := os.Open("/etc/resolv.conf")
	if err != nil {
		return "", errors.New("cannot read the system resolver config; specify dns_server")
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 && fields[0] == "nameserver" {
			return withPort(fields[1]), nil
		}
	}
	return "", errors.New("no nameserver in /etc/resolv.conf; specify dns_server")
}

func dnsQuery(ctx context.Context, fqdn string, rtype dnsmessage.Type, server, proto string, timeout time.Duration, ipVersion string, publicOnly bool) protocol.DNSAttempt {
	a := protocol.DNSAttempt{Server: server, Proto: proto, Answers: []protocol.DNSAnswer{}}
	qname, err := dnsmessage.NewName(fqdn)
	if err != nil {
		a.Error = "invalid name"
		return a
	}
	id := uint16(rand.IntN(0xffff))
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: id, RecursionDesired: true})
	b.EnableCompression()
	if err := b.StartQuestions(); err != nil {
		a.Error = err.Error()
		return a
	}
	if err := b.Question(dnsmessage.Question{Name: qname, Type: rtype, Class: dnsmessage.ClassINET}); err != nil {
		a.Error = err.Error()
		return a
	}
	msg, err := b.Finish()
	if err != nil {
		a.Error = err.Error()
		return a
	}

	qctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	var reply []byte
	switch proto {
	case "doh":
		reply, err = dohExchange(qctx, server, msg, publicOnly)
	case "tcp":
		reply, err = tcpExchange(qctx, server, msg, timeout, ipVersion, publicOnly)
	default:
		reply, err = udpExchange(qctx, server, msg, timeout, ipVersion, publicOnly)
		if err == nil && len(reply) >= 3 && reply[2]&0x02 != 0 { // TC bit: retry over TCP
			a.Truncated = true
			if r2, err2 := tcpExchange(qctx, server, msg, timeout, ipVersion, publicOnly); err2 == nil {
				reply, a.Proto = r2, "tcp"
			}
		}
	}
	a.RTTMs = ms(time.Since(start))
	if err != nil {
		a.Error = ErrString(err)
		a.RTTMs = 0
		return a
	}
	var parser dnsmessage.Parser
	hdr, err := parser.Start(reply)
	if err != nil {
		a.Error = "malformed reply: " + err.Error()
		return a
	}
	if hdr.ID != id {
		a.Error = "reply id mismatch"
		return a
	}
	a.RCode = strings.TrimPrefix(hdr.RCode.String(), "RCode")
	a.OK = hdr.RCode == dnsmessage.RCodeSuccess
	if err := parser.SkipAllQuestions(); err != nil {
		a.Error = err.Error()
		return a
	}
	for {
		h, err := parser.AnswerHeader()
		if errors.Is(err, dnsmessage.ErrSectionDone) {
			break
		}
		if err != nil {
			break
		}
		ans := protocol.DNSAnswer{Name: strings.TrimSuffix(h.Name.String(), "."), Type: strings.TrimPrefix(h.Type.String(), "Type"), TTL: h.TTL}
		switch h.Type {
		case dnsmessage.TypeA:
			r, err := parser.AResource()
			if err != nil {
				return a
			}
			ip := net.IP(r.A[:])
			ans.Value = ip.String()
			if IsFakeIP(ip) {
				a.FakeIP = true
			}
		case dnsmessage.TypeAAAA:
			r, err := parser.AAAAResource()
			if err != nil {
				return a
			}
			ans.Value = net.IP(r.AAAA[:]).String()
		case dnsmessage.TypeCNAME:
			r, err := parser.CNAMEResource()
			if err != nil {
				return a
			}
			ans.Value = strings.TrimSuffix(r.CNAME.String(), ".")
		case dnsmessage.TypeNS:
			r, err := parser.NSResource()
			if err != nil {
				return a
			}
			ans.Value = strings.TrimSuffix(r.NS.String(), ".")
		case dnsmessage.TypePTR:
			r, err := parser.PTRResource()
			if err != nil {
				return a
			}
			ans.Value = strings.TrimSuffix(r.PTR.String(), ".")
		case dnsmessage.TypeMX:
			r, err := parser.MXResource()
			if err != nil {
				return a
			}
			ans.Value = fmt.Sprintf("%d %s", r.Pref, strings.TrimSuffix(r.MX.String(), "."))
		case dnsmessage.TypeTXT:
			r, err := parser.TXTResource()
			if err != nil {
				return a
			}
			ans.Value = strings.Join(r.TXT, " ")
		case dnsmessage.TypeSOA:
			r, err := parser.SOAResource()
			if err != nil {
				return a
			}
			ans.Value = fmt.Sprintf("%s %s serial=%d", strings.TrimSuffix(r.NS.String(), "."), strings.TrimSuffix(r.MBox.String(), "."), r.Serial)
		case dnsmessage.TypeSRV:
			r, err := parser.SRVResource()
			if err != nil {
				return a
			}
			ans.Value = fmt.Sprintf("%d %d %d %s", r.Priority, r.Weight, r.Port, strings.TrimSuffix(r.Target.String(), "."))
		default:
			if err := parser.SkipAnswer(); err != nil {
				return a
			}
			continue
		}
		a.Answers = append(a.Answers, ans)
	}
	return a
}

func dialNetwork(base, ipVersion string) string {
	switch ipVersion {
	case "4":
		return base + "4"
	case "6":
		return base + "6"
	}
	return base
}

func udpExchange(ctx context.Context, server string, msg []byte, timeout time.Duration, ipVersion string, publicOnly bool) ([]byte, error) {
	d := net.Dialer{Timeout: timeout, Control: publicAddrControl(publicOnly)}
	conn, err := d.DialContext(ctx, dialNetwork("udp", ipVersion), server)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write(msg); err != nil {
		return nil, err
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

func tcpExchange(ctx context.Context, server string, msg []byte, timeout time.Duration, ipVersion string, publicOnly bool) ([]byte, error) {
	d := net.Dialer{Timeout: timeout, Control: publicAddrControl(publicOnly)}
	conn, err := d.DialContext(ctx, dialNetwork("tcp", ipVersion), server)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	framed := make([]byte, 2+len(msg))
	binary.BigEndian.PutUint16(framed, uint16(len(msg)))
	copy(framed[2:], msg)
	if _, err := conn.Write(framed); err != nil {
		return nil, err
	}
	var lenBuf [2]byte
	if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
		return nil, err
	}
	reply := make([]byte, binary.BigEndian.Uint16(lenBuf[:]))
	if _, err := io.ReadFull(conn, reply); err != nil {
		return nil, err
	}
	return reply, nil
}

func dohExchange(ctx context.Context, u string, msg []byte, publicOnly bool) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(msg))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")
	client := &http.Client{Transport: &http.Transport{
		Proxy:             nil,
		DisableKeepAlives: true,
		DialContext:       (&net.Dialer{Control: publicAddrControl(publicOnly)}).DialContext,
	}}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("doh: http %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 65535))
}

// publicAddrControl returns a net.Dialer.Control hook enforcing the public-only
// destination policy, or nil when the caller is trusted.
func publicAddrControl(publicOnly bool) func(string, string, syscall.RawConn) error {
	if !publicOnly {
		return nil
	}
	return func(_, address string, _ syscall.RawConn) error { return CheckPublicAddr(address) }
}

// checkPublicDNSServer rejects a caller-supplied resolver that is not on the
// public internet.
func checkPublicDNSServer(ctx context.Context, server, proto, ipVersion string) error {
	host := server
	if proto == "doh" {
		u, err := url.Parse(server)
		if err != nil {
			return fmt.Errorf("invalid DoH url: %w", err)
		}
		host = u.Hostname()
	} else if h, _, err := net.SplitHostPort(server); err == nil {
		host = h
	}
	_, err := Resolve(ctx, host, ipVersion, true)
	return err
}
