package probe

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"net"
	"strings"
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

func TestFakeIPDetection(t *testing.T) {
	for _, s := range []string{"198.18.0.1", "198.18.3.6", "198.19.255.254"} {
		if !IsFakeIP(net.ParseIP(s)) {
			t.Fatalf("%s should be fake-IP", s)
		}
	}
	for _, s := range []string{"198.17.255.255", "198.20.0.0", "223.5.5.5", "2001:db8::1"} {
		if IsFakeIP(net.ParseIP(s)) {
			t.Fatalf("%s should not be fake-IP", s)
		}
	}
	// A literal fake-IP target is allowed (the user asked for it explicitly).
	if _, err := Resolve(context.Background(), "198.18.3.6", ""); err != nil {
		t.Fatalf("literal fake-IP: %v", err)
	}
	if !strings.Contains(FakeIPError("www.qq.com", net.ParseIP("198.18.3.6")).Error(), "fake-IP 198.18.3.6") {
		t.Fatal("error text must name the address")
	}
}

func TestIsPermissionError(t *testing.T) {
	if !isPermissionError(errors.New("listen ip4:icmp 0.0.0.0: socket: operation not permitted")) {
		t.Fatal("socket open failure must count")
	}
	if isPermissionError(errors.New("write ip4 0.0.0.0->198.18.3.6: sendto: permission denied")) {
		t.Fatal("a refused sendto is not a privilege problem")
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

// TestInnerKey builds quoted datagrams like a router puts in a Time Exceeded
// message and checks we pull the right key back out for each probe flavour.
func TestInnerKey(t *testing.T) {
	const id, seq = 0xBEEF, 42
	ipHdr := func(ihl int, proto byte) []byte {
		h := make([]byte, ihl*4)
		h[0] = 0x40 | byte(ihl)
		h[9] = proto
		return h
	}
	icmpEcho := make([]byte, 8)
	icmpEcho[0] = 8
	binary.BigEndian.PutUint16(icmpEcho[4:], id)
	binary.BigEndian.PutUint16(icmpEcho[6:], seq)

	got, ok := innerKey(append(ipHdr(5, 1), icmpEcho...), false, "icmp", id)
	if !ok || got != seq {
		t.Fatalf("v4 icmp: got %d ok=%v", got, ok)
	}
	if _, ok := innerKey(append(ipHdr(5, 1), icmpEcho...), false, "icmp", id+1); ok {
		t.Fatal("wrong id must not match")
	}
	if got, ok := innerKey(append(ipHdr(6, 1), icmpEcho...), false, "icmp", id); !ok || got != seq {
		t.Fatalf("v4 ihl6: got %d ok=%v", got, ok)
	}

	udp := make([]byte, 8)
	binary.BigEndian.PutUint16(udp[0:], 41234) // src port
	binary.BigEndian.PutUint16(udp[2:], 33434)
	if got, ok := innerKey(append(ipHdr(5, 17), udp...), false, "udp", id); !ok || got != 41234 {
		t.Fatalf("v4 udp: got %d ok=%v", got, ok)
	}
	if _, ok := innerKey(append(ipHdr(5, 17), udp...), false, "tcp", id); ok {
		t.Fatal("udp quote must not match tcp mode")
	}
	tcp := make([]byte, 20)
	binary.BigEndian.PutUint16(tcp[0:], 50000)
	if got, ok := innerKey(append(ipHdr(5, 6), tcp...), false, "tcp", id); !ok || got != 50000 {
		t.Fatalf("v4 tcp: got %d ok=%v", got, ok)
	}

	// IPv6: fixed 40-byte header, next header at byte 6.
	ip6 := func(next byte) []byte { h := make([]byte, 40); h[6] = next; return h }
	icmp6 := make([]byte, 8)
	icmp6[0] = 128
	binary.BigEndian.PutUint16(icmp6[4:], id)
	binary.BigEndian.PutUint16(icmp6[6:], seq)
	if got, ok := innerKey(append(ip6(58), icmp6...), true, "icmp", id); !ok || got != seq {
		t.Fatalf("v6 icmp: got %d ok=%v", got, ok)
	}
	if got, ok := innerKey(append(ip6(6), tcp...), true, "tcp", id); !ok || got != 50000 {
		t.Fatalf("v6 tcp: got %d ok=%v", got, ok)
	}
	if _, ok := innerKey([]byte{1, 2, 3}, false, "icmp", id); ok {
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

func TestHTTPAssertionsAndSpeed(t *testing.T) {
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
				n, _ := c.Read(buf)
				req := string(buf[:n])
				switch {
				case strings.HasPrefix(req, "GET /redir"):
					_, _ = c.Write([]byte("HTTP/1.1 302 Found\r\nLocation: /ok\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
				case strings.HasPrefix(req, "GET /big"):
					_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Type: application/octet-stream\r\nConnection: close\r\n\r\n"))
					chunk := make([]byte, 64<<10)
					deadline := time.Now().Add(3 * time.Second)
					for time.Now().Before(deadline) {
						if _, err := c.Write(chunk); err != nil {
							return
						}
					}
				default:
					_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 11\r\nConnection: close\r\n\r\nhello world"))
				}
			}(c)
		}
	}()
	base := "http://" + ln.Addr().String()

	// Keyword + implicit status assertion pass; max-time assertion fails.
	res, err := HTTP(context.Background(), base+"/ok", protocol.Params{ExpectKeyword: "world", ExpectMaxMs: 0}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := res.Attempts[0]
	if !a.OK || !a.AssertOK || len(a.Assertions) != 2 || !a.Assertions[1].Pass {
		t.Fatalf("keyword: %+v", a.Assertions)
	}
	res, _ = HTTP(context.Background(), base+"/ok", protocol.Params{ExpectKeyword: "nope", ExpectStatus: 201}, nil)
	a = res.Attempts[0]
	if a.AssertOK || a.Assertions[0].Pass || a.Assertions[1].Pass {
		t.Fatalf("failing assertions: %+v", a.Assertions)
	}
	if res.Stats.Received != 0 {
		t.Fatal("assertion failures must not count as successes")
	}

	// Redirect chain records the status of each hop.
	res, _ = HTTP(context.Background(), base+"/redir", protocol.Params{FollowRedirects: true}, nil)
	a = res.Attempts[0]
	if !a.OK || a.StatusCode != 200 || len(a.Redirects) != 1 || !strings.HasPrefix(a.Redirects[0], "302 ") {
		t.Fatalf("redirects: %+v status=%d", a.Redirects, a.StatusCode)
	}

	// Speed test reads for the window and reports throughput.
	res, _ = HTTP(context.Background(), base+"/big", protocol.Params{SpeedTest: true, SpeedSeconds: 1}, nil)
	a = res.Attempts[0]
	// On loopback the byte cap is reached long before the 1 s window; on a
	// real link the window ends first. Either way we must have a rate.
	if !a.OK || a.SpeedBytes < 64<<10 || a.ThroughputMbps <= 0 || (a.SpeedMs < 900 && a.SpeedBytes < maxSpeedRead) {
		t.Fatalf("speed: bytes=%d mbps=%v ms=%v err=%s", a.SpeedBytes, a.ThroughputMbps, a.SpeedMs, a.Error)
	}
}
