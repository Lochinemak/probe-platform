package probe

import (
	"context"
	"net"
	"testing"

	"golang.org/x/net/dns/dnsmessage"

	"probe-platform/internal/protocol"
)

// fakeDNS answers every A question with the given IP and every other type with NXDOMAIN.
func fakeDNS(t *testing.T, ip [4]byte, ttl uint32) string {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			var p dnsmessage.Parser
			hdr, err := p.Start(buf[:n])
			if err != nil {
				continue
			}
			q, err := p.Question()
			if err != nil {
				continue
			}
			rcode := dnsmessage.RCodeSuccess
			if q.Type != dnsmessage.TypeA {
				rcode = dnsmessage.RCodeNameError
			}
			b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: hdr.ID, Response: true, RecursionAvailable: true, RCode: rcode})
			_ = b.StartQuestions()
			_ = b.Question(q)
			_ = b.StartAnswers()
			if rcode == dnsmessage.RCodeSuccess {
				_ = b.AResource(dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: ttl}, dnsmessage.AResource{A: ip})
			}
			out, _ := b.Finish()
			_, _ = pc.WriteTo(out, addr)
		}
	}()
	return pc.LocalAddr().String()
}

func TestDNSProbe(t *testing.T) {
	server := fakeDNS(t, [4]byte{93, 184, 216, 34}, 300)
	res, err := DNS(context.Background(), "example.com.", protocol.Params{DNSServer: server, Count: 2, IntervalMs: 10}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Attempts) != 2 || res.RecordType != "A" || res.Target != "example.com" {
		t.Fatalf("result: %+v", res)
	}
	a := res.Attempts[0]
	if !a.OK || a.RCode != "Success" || a.Proto != "udp" || a.Server != server {
		t.Fatalf("attempt: %+v", a)
	}
	if len(a.Answers) != 1 || a.Answers[0].Value != "93.184.216.34" || a.Answers[0].TTL != 300 || a.Answers[0].Type != "A" {
		t.Fatalf("answers: %+v", a.Answers)
	}
	if a.FakeIP {
		t.Fatal("real IP flagged as fake")
	}
	if res.Stats.Received != 2 || a.RTTMs <= 0 {
		t.Fatalf("stats: %+v rtt=%v", res.Stats, a.RTTMs)
	}

	// NXDOMAIN is a completed query with OK=false and rcode reported.
	res, err = DNS(context.Background(), "example.com", protocol.Params{DNSServer: server, RecordType: "AAAA"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a := res.Attempts[0]; a.OK || a.RCode != "NameError" || len(a.Answers) != 0 {
		t.Fatalf("nxdomain attempt: %+v", a)
	}
}

func TestDNSFakeIPFlag(t *testing.T) {
	server := fakeDNS(t, [4]byte{198, 18, 3, 6}, 1)
	res, err := DNS(context.Background(), "www.qq.com", protocol.Params{DNSServer: server}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a := res.Attempts[0]; !a.OK || !a.FakeIP {
		t.Fatalf("fake-IP must be flagged, not rejected: %+v", a)
	}
}

func TestDNSServerSpec(t *testing.T) {
	cases := map[string][2]string{
		"223.5.5.5":                 {"223.5.5.5:53", "udp"},
		"223.5.5.5:5353":            {"223.5.5.5:5353", "udp"},
		"tcp://1.1.1.1":             {"1.1.1.1:53", "tcp"},
		"[2400:3200::1]":            {"[2400:3200::1]:53", "udp"},
		"https://doh.pub/dns-query": {"https://doh.pub/dns-query", "doh"},
		"udp://119.29.29.29":        {"119.29.29.29:53", "udp"},
	}
	for in, want := range cases {
		s, proto, err := dnsServer(in)
		if err != nil || s != want[0] || proto != want[1] {
			t.Errorf("%q: got (%s,%s,%v) want %v", in, s, proto, err, want)
		}
	}
	if _, _, err := dnsType("BOGUS"); err == nil {
		t.Fatal("bogus type must error")
	}
}
