package probe

import (
	"context"
	"encoding/binary"
	"math"
	"net"
	"testing"
	"time"

	"probe-platform/internal/protocol"
)

func TestSplitTarget(t *testing.T) {
	cases := []struct {
		in       string
		fallback int
		host     string
		port     int
		wantErr  bool
	}{
		{"example.com", 80, "example.com", 80, false},
		{"example.com:443", 80, "example.com", 443, false},
		{"https://example.com/path", 80, "example.com", 80, false},
		{"https://example.com:8443/x", 80, "example.com", 8443, false},
		{"[2001:db8::1]:22", 80, "2001:db8::1", 22, false},
		{"2001:db8::1", 80, "2001:db8::1", 80, false},
		{"example.com", 0, "", 0, true},
		{"example.com:99999", 80, "", 0, true},
	}
	for _, c := range cases {
		host, port, err := SplitTarget(c.in, c.fallback)
		if (err != nil) != c.wantErr {
			t.Fatalf("%q: err=%v wantErr=%v", c.in, err, c.wantErr)
		}
		if err == nil && (host != c.host || port != c.port) {
			t.Fatalf("%q: got %s:%d want %s:%d", c.in, host, port, c.host, c.port)
		}
	}
}

func TestComputeStats(t *testing.T) {
	s := ComputeStats(4, []float64{10, 20, 30})
	if s.Sent != 4 || s.Received != 3 || s.LossPct != 25 {
		t.Fatalf("counts: %+v", s)
	}
	if s.MinMs != 10 || s.MaxMs != 30 || s.AvgMs != 20 {
		t.Fatalf("min/avg/max: %+v", s)
	}
	if math.Abs(s.StdDevMs-8.165) > 0.001 {
		t.Fatalf("stddev: %v", s.StdDevMs)
	}
	empty := ComputeStats(3, nil)
	if empty.LossPct != 100 || empty.Received != 0 {
		t.Fatalf("empty: %+v", empty)
	}
}

func TestResolveLiteral(t *testing.T) {
	ip, err := Resolve(context.Background(), "127.0.0.1", "")
	if err != nil || !ip.Equal(net.ParseIP("127.0.0.1")) {
		t.Fatalf("v4 literal: %v %v", ip, err)
	}
	if _, err := Resolve(context.Background(), "127.0.0.1", "6"); err == nil {
		t.Fatal("expected error forcing v6 on a v4 literal")
	}
	if _, err := Resolve(context.Background(), "::1", "4"); err == nil {
		t.Fatal("expected error forcing v4 on a v6 literal")
	}
}

// TestInnerSeq builds a quoted IPv4 datagram like a router puts in a Time
// Exceeded message and checks we pull the right ID/seq back out.
func TestInnerSeq(t *testing.T) {
	const id, seq = 0xBEEF, 42
	ipHdr := make([]byte, 20)
	ipHdr[0] = 0x45 // IPv4, IHL 5
	icmpHdr := make([]byte, 8)
	icmpHdr[0] = 8 // echo request
	binary.BigEndian.PutUint16(icmpHdr[4:], id)
	binary.BigEndian.PutUint16(icmpHdr[6:], seq)
	data := append(ipHdr, icmpHdr...)

	got, ok := innerSeq(data, false, id)
	if !ok || got != seq {
		t.Fatalf("v4: got %d ok=%v", got, ok)
	}
	if _, ok := innerSeq(data, false, id+1); ok {
		t.Fatal("v4: wrong id must not match")
	}
	// IHL 6 (one option word) shifts the ICMP header by 4 bytes.
	ipHdr6 := make([]byte, 24)
	ipHdr6[0] = 0x46
	got, ok = innerSeq(append(ipHdr6, icmpHdr...), false, id)
	if !ok || got != seq {
		t.Fatalf("v4 ihl6: got %d ok=%v", got, ok)
	}

	// IPv6: fixed 40-byte header, echo request type 128.
	ip6 := make([]byte, 40)
	icmp6 := make([]byte, 8)
	icmp6[0] = 128
	binary.BigEndian.PutUint16(icmp6[4:], id)
	binary.BigEndian.PutUint16(icmp6[6:], seq)
	got, ok = innerSeq(append(ip6, icmp6...), true, id)
	if !ok || got != seq {
		t.Fatalf("v6: got %d ok=%v", got, ok)
	}
	if _, ok := innerSeq([]byte{1, 2, 3}, false, id); ok {
		t.Fatal("short buffer must not match")
	}
}

func TestTCPingLoopback(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback listener:", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	var seen int
	var resolved string
	res, err := TCPing(context.Background(), ln.Addr().String(), protocol.Params{Count: 3, IntervalMs: 10}, func(kind string, data any) {
		switch kind {
		case "reply":
			seen++
		case "resolved":
			resolved = data.(string)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats.Sent != 3 || res.Stats.Received != 3 || seen != 3 {
		t.Fatalf("stats %+v seen=%d", res.Stats, seen)
	}
	if resolved != "127.0.0.1" {
		t.Fatalf("resolved event: %q", resolved)
	}
	if res.Stats.LossPct != 0 {
		t.Fatalf("loss %v", res.Stats.LossPct)
	}
}

func TestTCPingRefused(t *testing.T) {
	// Grab a free port then close it so the connect is refused.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	res, err := TCPing(context.Background(), addr, protocol.Params{Count: 2, IntervalMs: 10, TimeoutMs: 500}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats.Received != 0 || res.Stats.LossPct != 100 {
		t.Fatalf("expected total loss: %+v", res.Stats)
	}
	if res.Replies[0].Error == "" {
		t.Fatal("expected an error string on refused connect")
	}
}

func TestHTTPProbeLocal(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 4096)
				_ = c.SetReadDeadline(time.Now().Add(time.Second))
				_, _ = c.Read(buf)
				_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 5\r\nServer: test\r\nConnection: close\r\n\r\nhello"))
			}(c)
		}
	}()
	res, err := HTTP(context.Background(), "http://"+ln.Addr().String()+"/x", protocol.Params{Count: 2, IntervalMs: 10}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Attempts) != 2 {
		t.Fatalf("attempts %d", len(res.Attempts))
	}
	a := res.Attempts[0]
	if !a.OK || a.StatusCode != 200 || a.BodyBytes != 5 || a.Headers["Server"] != "test" {
		t.Fatalf("attempt: %+v", a)
	}
	if a.Timing.TotalMs <= 0 || a.Timing.ConnectMs < 0 || a.Timing.TTFBMs < 0 {
		t.Fatalf("timing: %+v", a.Timing)
	}
	if a.IP != "127.0.0.1" {
		t.Fatalf("ip: %q", a.IP)
	}
	if res.Stats.Received != 2 {
		t.Fatalf("stats: %+v", res.Stats)
	}
}

func TestEffectiveEnd(t *testing.T) {
	hops := make([]*hopAcc, 31)
	for i := range hops {
		hops[i] = &hopAcc{ttl: i}
	}
	if got := effectiveEnd(hops, 30, 7); got != 7 {
		t.Fatalf("reached: %d", got)
	}
	hops[5].recv = 1
	if got := effectiveEnd(hops, 30, 0); got != 7 {
		t.Fatalf("last responding +2: %d", got)
	}
	if got := effectiveEnd(hops, 6, 0); got != 6 {
		t.Fatalf("capped at maxHops: %d", got)
	}
}
