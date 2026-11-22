package probe

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"probe-platform/internal/protocol"
)

// Record types beyond the ones dnsmessage has a Go type for. They are asked
// for by number and their data is decoded here.
const (
	typeDS     dnsmessage.Type = 43
	typeDNSKEY dnsmessage.Type = 48
	typeTLSA   dnsmessage.Type = 52
	typeSVCB   dnsmessage.Type = 64
	typeHTTPS  dnsmessage.Type = 65
	typeCAA    dnsmessage.Type = 257
)

var extraTypes = map[string]dnsmessage.Type{"DS": typeDS, "DNSKEY": typeDNSKEY, "TLSA": typeTLSA, "SVCB": typeSVCB, "HTTPS": typeHTTPS, "CAA": typeCAA}

func typeName(t dnsmessage.Type) string {
	for name, v := range extraTypes {
		if v == t {
			return name
		}
	}
	return strings.TrimPrefix(t.String(), "Type")
}

// recordOf renders one resource record as the dashboard shows it. ok is false
// for a record there is nothing sensible to show for (OPT and the like).
func recordOf(rr dnsmessage.Resource) (protocol.DNSAnswer, bool) {
	ans := protocol.DNSAnswer{Name: strings.TrimSuffix(rr.Header.Name.String(), "."), Type: typeName(rr.Header.Type), TTL: rr.Header.TTL}
	name := func(n dnsmessage.Name) string { return strings.TrimSuffix(n.String(), ".") }
	switch b := rr.Body.(type) {
	case *dnsmessage.AResource:
		ans.Value = net.IP(b.A[:]).String()
	case *dnsmessage.AAAAResource:
		ans.Value = net.IP(b.AAAA[:]).String()
	case *dnsmessage.CNAMEResource:
		ans.Value = name(b.CNAME)
	case *dnsmessage.NSResource:
		ans.Value = name(b.NS)
	case *dnsmessage.PTRResource:
		ans.Value = name(b.PTR)
	case *dnsmessage.MXResource:
		ans.Value = fmt.Sprintf("%d %s", b.Pref, name(b.MX))
	case *dnsmessage.TXTResource:
		ans.Value = strings.Join(b.TXT, " ")
	case *dnsmessage.SOAResource:
		ans.Value = fmt.Sprintf("%s %s serial=%d", name(b.NS), name(b.MBox), b.Serial)
	case *dnsmessage.SRVResource:
		ans.Value = fmt.Sprintf("%d %d %d %s", b.Priority, b.Weight, b.Port, name(b.Target))
	case *dnsmessage.UnknownResource:
		ans.Value = unknownRData(rr.Header.Type, b.Data)
	default:
		return ans, false
	}
	return ans, true
}

// unknownRData decodes the record types extraTypes lists, and falls back to
// the generic "\# length hex" form of RFC 3597 for anything else.
func unknownRData(t dnsmessage.Type, d []byte) string {
	generic := func() string { return fmt.Sprintf("\\# %d %s", len(d), hex.EncodeToString(d)) }
	switch t {
	case typeCAA: // flags, tag length, tag, value
		if len(d) < 2 || int(d[1]) > len(d)-2 {
			return generic()
		}
		tag := string(d[2 : 2+int(d[1])])
		return fmt.Sprintf("%d %s %q", d[0], tag, string(d[2+int(d[1]):]))
	case typeDS: // key tag, algorithm, digest type, digest
		if len(d) < 4 {
			return generic()
		}
		return fmt.Sprintf("%d %d %d %s", binary.BigEndian.Uint16(d), d[2], d[3], strings.ToUpper(hex.EncodeToString(d[4:])))
	case typeDNSKEY: // flags, protocol, algorithm, public key
		if len(d) < 4 {
			return generic()
		}
		return fmt.Sprintf("%d %d %d %s", binary.BigEndian.Uint16(d), d[2], d[3], base64.StdEncoding.EncodeToString(d[4:]))
	case typeTLSA: // usage, selector, matching type, association data
		if len(d) < 3 {
			return generic()
		}
		return fmt.Sprintf("%d %d %d %s", d[0], d[1], d[2], hex.EncodeToString(d[3:]))
	case typeSVCB, typeHTTPS: // priority, target name, then key=value parameters
		if len(d) < 3 {
			return generic()
		}
		target, n, ok := wireName(d[2:])
		if !ok {
			return generic()
		}
		out := fmt.Sprintf("%d %s", binary.BigEndian.Uint16(d), target)
		rest := d[2+n:]
		for len(rest) >= 4 {
			key, l := binary.BigEndian.Uint16(rest), int(binary.BigEndian.Uint16(rest[2:]))
			if l > len(rest)-4 {
				return generic()
			}
			out += " " + svcParam(key, rest[4:4+l])
			rest = rest[4+l:]
		}
		return out
	}
	return generic()
}

// wireName reads an uncompressed domain name (SVCB targets may not be
// compressed) and returns it with the number of bytes it took.
func wireName(d []byte) (string, int, bool) {
	var labels []string
	i := 0
	for {
		if i >= len(d) {
			return "", 0, false
		}
		l := int(d[i])
		i++
		if l == 0 {
			break
		}
		if l > 63 || i+l > len(d) {
			return "", 0, false
		}
		labels = append(labels, string(d[i:i+l]))
		i += l
	}
	if len(labels) == 0 {
		return ".", i, true
	}
	return strings.Join(labels, "."), i, true
}

func svcParam(key uint16, v []byte) string {
	switch key {
	case 1: // alpn: length-prefixed protocol ids
		var ids []string
		for len(v) > 0 && int(v[0]) < len(v) {
			ids = append(ids, string(v[1:1+int(v[0])]))
			v = v[1+int(v[0]):]
		}
		return "alpn=" + strings.Join(ids, ",")
	case 2:
		return "no-default-alpn"
	case 3:
		if len(v) == 2 {
			return fmt.Sprintf("port=%d", binary.BigEndian.Uint16(v))
		}
	case 4, 6: // ipv4hint, ipv6hint
		size, label := 4, "ipv4hint="
		if key == 6 {
			size, label = 16, "ipv6hint="
		}
		var ips []string
		for ; len(v) >= size; v = v[size:] {
			ips = append(ips, net.IP(v[:size]).String())
		}
		return label + strings.Join(ips, ",")
	case 5:
		return "ech=" + base64.StdEncoding.EncodeToString(v)
	}
	return fmt.Sprintf("key%d=%s", key, hex.EncodeToString(v))
}

// DNSMessage is one DNS response in full, for callers that need more than the
// answer section: the propagation check walks referrals (Authority and
// Additional) and looks at the AA bit.
type DNSMessage struct {
	RCode         string
	Authoritative bool
	Truncated     bool
	RTTMs         float64
	Answers       []protocol.DNSAnswer
	Authority     []protocol.DNSAnswer
	Additional    []protocol.DNSAnswer
}

// DNSQuestion is what DNSExchange asks.
type DNSQuestion struct {
	Name string // with or without the trailing dot
	Type string // "A", "NS", "CAA" …
	// Recurse sets the RD bit: on for a recursive resolver, off when asking
	// an authoritative server what it itself holds.
	Recurse bool
	// Server is "ip", "ip:port" or "[v6]:port"; UDP with a TCP retry when the
	// answer is truncated.
	Server     string
	Timeout    time.Duration
	PublicOnly bool
}

// DNSExchange sends one question to one server and returns the whole reply.
func DNSExchange(ctx context.Context, q DNSQuestion) (*DNSMessage, error) {
	rtype, _, err := dnsType(q.Type)
	if err != nil {
		return nil, err
	}
	qname, err := dnsmessage.NewName(strings.TrimSuffix(strings.TrimSpace(q.Name), ".") + ".")
	if err != nil {
		return nil, errors.New("invalid name")
	}
	if q.Timeout <= 0 {
		q.Timeout = 3 * time.Second
	}
	id := uint16(rand.IntN(0xffff))
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: id, RecursionDesired: q.Recurse})
	b.EnableCompression()
	if err := b.StartQuestions(); err != nil {
		return nil, err
	}
	if err := b.Question(dnsmessage.Question{Name: qname, Type: rtype, Class: dnsmessage.ClassINET}); err != nil {
		return nil, err
	}
	msg, err := b.Finish()
	if err != nil {
		return nil, err
	}
	server := withPort(q.Server)
	qctx, cancel := context.WithTimeout(ctx, q.Timeout)
	defer cancel()
	start := time.Now()
	reply, err := udpExchange(qctx, server, msg, q.Timeout, "", q.PublicOnly)
	out := &DNSMessage{}
	if err == nil && len(reply) >= 3 && reply[2]&0x02 != 0 {
		out.Truncated = true
		if r2, err2 := tcpExchange(qctx, server, msg, q.Timeout, "", q.PublicOnly); err2 == nil {
			reply = r2
		}
	}
	if err != nil {
		return nil, shortErr(err)
	}
	out.RTTMs = ms(time.Since(start))
	var parser dnsmessage.Parser
	hdr, err := parser.Start(reply)
	if err != nil {
		return nil, errors.New("malformed reply")
	}
	if hdr.ID != id {
		return nil, errors.New("reply id mismatch")
	}
	out.RCode = strings.TrimPrefix(hdr.RCode.String(), "RCode")
	out.Authoritative = hdr.Authoritative
	if err := parser.SkipAllQuestions(); err != nil {
		return nil, errors.New("malformed reply")
	}
	collect := func(rrs []dnsmessage.Resource) []protocol.DNSAnswer {
		var list []protocol.DNSAnswer
		for _, rr := range rrs {
			if ans, ok := recordOf(rr); ok {
				list = append(list, ans)
			}
		}
		return list
	}
	// A section that fails to parse ends the reading; what came before stands.
	if rrs, err := parser.AllAnswers(); err == nil {
		out.Answers = collect(rrs)
		if rrs, err := parser.AllAuthorities(); err == nil {
			out.Authority = collect(rrs)
			if rrs, err := parser.AllAdditionals(); err == nil {
				out.Additional = collect(rrs)
			}
		}
	}
	return out, nil
}
