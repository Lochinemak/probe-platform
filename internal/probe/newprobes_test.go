package probe

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"probe-platform/internal/protocol"
)

// udpEcho answers every datagram with itself.
func udpEcho(t *testing.T) (host string, port int) {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = pc.WriteTo(buf[:n], addr)
		}
	}()
	a := pc.LocalAddr().(*net.UDPAddr)
	return a.IP.String(), a.Port
}

func TestUDPProbe(t *testing.T) {
	host, port := udpEcho(t)
	target := net.JoinHostPort(host, strconv.Itoa(port))
	var kinds []string
	res, err := UDP(context.Background(), target, protocol.Params{Count: 3, IntervalMs: 10, Payload: "hello"}, func(k string, _ any) { kinds = append(kinds, k) })
	if err != nil {
		t.Fatal(err)
	}
	if res.Proto != "udp" || res.IP != host || res.Port != port || len(res.Replies) != 3 || res.Stats.Received != 3 || res.Stats.LossPct != 0 {
		t.Fatalf("echo: %+v", res)
	}
	if r := res.Replies[0]; !r.OK || r.State != "reply" || r.Size != len("hello") || r.RTTMs <= 0 {
		t.Fatalf("reply: %+v", r)
	}
	if strings.Join(kinds, ",") != "resolved,reply,reply,reply" {
		t.Fatalf("progress: %v", kinds)
	}

	// Sending only: nothing is waited for, so a dead port "succeeds" too.
	res, err = UDP(context.Background(), target, protocol.Params{Count: 1, NoWait: true}, nil)
	if err != nil || len(res.Replies) != 1 || !res.Replies[0].OK || res.Replies[0].State != "sent" {
		t.Fatalf("no_wait: %v %+v", err, res)
	}

	// A port nothing listens on: the kernel answers with ICMP port
	// unreachable, which the probe tells apart from silence.
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	dead := pc.LocalAddr().(*net.UDPAddr).Port
	pc.Close()
	res, err = UDP(context.Background(), net.JoinHostPort("127.0.0.1", strconv.Itoa(dead)), protocol.Params{Count: 1, TimeoutMs: 500}, nil)
	if err != nil || len(res.Replies) != 1 {
		t.Fatalf("closed port: %v %+v", err, res)
	}
	if r := res.Replies[0]; r.OK || (r.State != "closed" && r.State != "no_reply") {
		t.Fatalf("closed port reply: %+v", r)
	} else if r.State == "no_reply" && runtime.GOOS == "linux" {
		t.Errorf("linux reports a closed loopback port as unreachable, got %+v", r)
	}

	if _, err := UDP(context.Background(), "127.0.0.1", protocol.Params{}, nil); err == nil {
		t.Error("a target without a port must be refused")
	}
	if _, err := UDP(context.Background(), target, protocol.Params{PublicOnly: true}, nil); err == nil {
		t.Error("public-only must refuse loopback")
	}
	if _, err := UDP(context.Background(), target, protocol.Params{PayloadMode: "text", Payload: strings.Repeat("x", 2000)}, nil); err == nil {
		t.Error("oversized payload must be refused")
	}
}

func TestUDPPayload(t *testing.T) {
	if b, err := udpPayload(protocol.Params{}, 53); err != nil || len(b) < 12 || b[2]&0x01 == 0 {
		t.Fatalf("port 53 defaults to a recursive DNS query: %v %x", err, b)
	}
	if b, _ := udpPayload(protocol.Params{}, 123); string(b) != "probe" {
		t.Fatalf("default text payload: %q", b)
	}
	if b, _ := udpPayload(protocol.Params{PayloadMode: "dns", QueryName: "www.example.org"}, 5353); !strings.Contains(string(b), "example") {
		t.Fatalf("dns payload: %x", b)
	}
	if _, err := udpPayload(protocol.Params{PayloadMode: "bogus"}, 1); err == nil {
		t.Error("unknown payload mode accepted")
	}
}

func TestTLSProbe(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	var kinds []string
	res, err := TLS(context.Background(), u.Host, protocol.Params{Count: 2, IntervalMs: 10, ExpectCertDays: 100000}, func(k string, _ any) { kinds = append(kinds, k) })
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Attempts) != 2 || !res.Attempts[0].OK || res.Attempts[0].HandshakeMs <= 0 || res.Stats.Received != 2 {
		t.Fatalf("attempts: %+v stats %+v", res.Attempts, res.Stats)
	}
	if !strings.HasPrefix(res.Version, "TLS 1.") || res.Cipher == "" || res.IP != "127.0.0.1" || strconv.Itoa(res.Port) != u.Port() {
		t.Fatalf("negotiated: %+v", res)
	}
	// httptest's certificate is signed by its own throwaway CA: the chain is
	// reported in full although it does not verify here.
	if len(res.Chain) == 0 || res.Verified || res.VerifyError == "" {
		t.Fatalf("an untrusted chain must be shown and flagged: verified=%v err=%q chain=%d", res.Verified, res.VerifyError, len(res.Chain))
	}
	leaf := res.Chain[0]
	if leaf.Subject == "" || leaf.KeyType == "" || leaf.NotAfter.IsZero() || len(leaf.SANs) == 0 {
		t.Fatalf("leaf: %+v", leaf)
	}
	if res.DaysLeft <= 0 || res.CertOK {
		t.Fatalf("days_left=%d cert_ok=%v with an absurd expect_cert_days", res.DaysLeft, res.CertOK)
	}
	if strings.Join(kinds, ",") != "resolved,attempt,attempt" {
		t.Fatalf("progress: %v", kinds)
	}

	// Not TLS at all: the attempt fails, the probe still returns.
	plain := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer plain.Close()
	pu, _ := url.Parse(plain.URL)
	res, err = TLS(context.Background(), pu.Host, protocol.Params{TimeoutMs: 2000}, nil)
	if err != nil || len(res.Attempts) != 1 || res.Attempts[0].OK || res.Attempts[0].Error == "" || len(res.Chain) != 0 {
		t.Fatalf("plain HTTP port: %v %+v", err, res)
	}
	if _, err := TLS(context.Background(), u.Host, protocol.Params{PublicOnly: true}, nil); err == nil {
		t.Error("public-only must refuse loopback")
	}
}

// TestConnectTo: the probe goes to the given address while the name it was
// asked about stays the TLS server name.
func TestConnectTo(t *testing.T) {
	var sni string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(r.Host)) }))
	srv.TLS = &tls.Config{GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) { sni = h.ServerName; return nil, nil }}
	srv.StartTLS()
	defer srv.Close()
	u, _ := url.Parse(srv.URL)

	res, err := TLS(context.Background(), "name.invalid:"+u.Port(), protocol.Params{ConnectTo: "127.0.0.1"}, nil)
	if err != nil || len(res.Attempts) != 1 || !res.Attempts[0].OK {
		t.Fatalf("tls via connect_to: %v %+v", err, res)
	}
	if sni != "name.invalid" || res.ServerName != "name.invalid" || res.IP != "127.0.0.1" {
		t.Fatalf("sni=%q server_name=%q ip=%q", sni, res.ServerName, res.IP)
	}

	tcp, err := TCPing(context.Background(), "name.invalid:"+u.Port(), protocol.Params{Count: 1, ConnectTo: "127.0.0.1"}, nil)
	if err != nil || tcp.IP != "127.0.0.1" || !tcp.Replies[0].OK {
		t.Fatalf("tcping via connect_to: %v %+v", err, tcp)
	}

	sni = ""
	hr, err := HTTP(context.Background(), "https://name.invalid:"+u.Port()+"/", protocol.Params{ConnectTo: "127.0.0.1", InsecureTLS: true, TimeoutMs: 3000}, nil)
	if err != nil || len(hr.Attempts) != 1 {
		t.Fatalf("http via connect_to: %v", err)
	}
	if a := hr.Attempts[0]; !a.OK || a.IP != "127.0.0.1" || sni != "name.invalid" {
		t.Fatalf("http attempt: ok=%v ip=%q err=%q sni=%q", a.OK, a.IP, a.Error, sni)
	}
}

// TestResolver: the target is looked up at the given DNS server, not the
// system's.
func TestResolver(t *testing.T) {
	dns := fakeDNS(t, [4]byte{127, 0, 0, 1}, 60)
	ip, err := ResolveTarget(context.Background(), "whatever.invalid", protocol.Params{Resolver: dns})
	if err != nil || ip.String() != "127.0.0.1" {
		t.Fatalf("resolve via %s: %v %v", dns, ip, err)
	}
	// IPv6 only: the fake server has no AAAA.
	if _, err := ResolveTarget(context.Background(), "whatever.invalid", protocol.Params{Resolver: dns, IPVersion: "6"}); err == nil {
		t.Error("a name without AAAA resolved for IPv6")
	}
	// A literal address needs no resolver; "system" means the default.
	if ip, err := ResolveTarget(context.Background(), "192.0.2.7", protocol.Params{Resolver: dns}); err != nil || ip.String() != "192.0.2.7" {
		t.Errorf("literal: %v %v", ip, err)
	}
	// The policy still applies to what the resolver returns, and to the
	// resolver itself.
	if _, err := ResolveTarget(context.Background(), "whatever.invalid", protocol.Params{Resolver: dns, PublicOnly: true}); err == nil {
		t.Error("public-only accepted a loopback resolver")
	}
	fake := fakeDNS(t, [4]byte{198, 18, 0, 9}, 60)
	if _, err := ResolveTarget(context.Background(), "whatever.invalid", protocol.Params{Resolver: fake}); err == nil || !strings.Contains(err.Error(), "fake-IP") {
		t.Errorf("fake-IP answer from the chosen resolver: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	hr, err := HTTP(context.Background(), "http://resolved.invalid:"+u.Port()+"/", protocol.Params{Resolver: dns, TimeoutMs: 3000}, nil)
	if err != nil || len(hr.Attempts) != 1 || !hr.Attempts[0].OK || hr.Attempts[0].IP != "127.0.0.1" {
		t.Fatalf("http via resolver: %v %+v", err, hr.Attempts)
	}
}

func TestDNSCompare(t *testing.T) {
	a := fakeDNS(t, [4]byte{93, 184, 216, 34}, 300)
	b := fakeDNS(t, [4]byte{93, 184, 216, 34}, 60) // same answer, different TTL
	c := fakeDNS(t, [4]byte{203, 0, 113, 9}, 300)
	var entries int
	res, err := DNSCompare(context.Background(), "example.com", protocol.Params{DNSServers: []string{a, b, a}}, func(k string, _ any) {
		if k == "entry" {
			entries++
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 2 || entries != 2 || res.Verdict != "consistent" || res.Groups != 1 {
		t.Fatalf("same answers (duplicates dropped): %+v", res)
	}
	if res.Entries[0].Server != a || res.Entries[0].Key != "93.184.216.34" || !res.Entries[0].Attempt.OK {
		t.Fatalf("entry: %+v", res.Entries[0])
	}
	res, err = DNSCompare(context.Background(), "example.com", protocol.Params{DNSServers: []string{a, c}}, nil)
	if err != nil || res.Verdict != "inconsistent" || res.Groups != 2 {
		t.Fatalf("different answers: %v %+v", err, res)
	}
	// A resolver that fails is listed and left out of the verdict.
	res, err = DNSCompare(context.Background(), "example.com", protocol.Params{DNSServers: []string{a, "127.0.0.1:1"}, TimeoutMs: 500}, nil)
	if err != nil || res.Verdict != "consistent" || res.Entries[1].Attempt.OK || res.Entries[1].Attempt.Error == "" {
		t.Fatalf("one resolver down: %v %+v", err, res)
	}
	res, err = DNSCompare(context.Background(), "example.com", protocol.Params{RecordType: "AAAA", DNSServers: []string{a}}, nil)
	if err != nil || res.Verdict != "failed" {
		t.Fatalf("nobody answered: %v %+v", err, res)
	}
	if _, err := DNSCompare(context.Background(), "example.com", protocol.Params{DNSServers: make([]string, 0)}, nil); err != nil {
		t.Errorf("no resolvers means the system one: %v", err)
	}
	many := []string{}
	for i := 0; i < 9; i++ {
		many = append(many, "192.0.2."+strconv.Itoa(i+1))
	}
	if _, err := DNSCompare(context.Background(), "example.com", protocol.Params{DNSServers: many}, nil); err == nil {
		t.Error("nine resolvers accepted")
	}
	res, err = DNSCompare(context.Background(), "example.com", protocol.Params{DNSServers: []string{a}, PublicOnly: true}, nil)
	if err != nil || res.Entries[0].Attempt.OK {
		t.Fatalf("public-only must not query a loopback resolver: %v %+v", err, res)
	}
}

func TestStatusProbe(t *testing.T) {
	res, err := Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.OS != runtime.GOOS || res.Arch != runtime.GOARCH || res.CPUs < 1 || res.GoVersion == "" || res.DiskPath == "" || len(res.Capabilities) == 0 {
		t.Fatalf("portable fields: %+v", res)
	}
	switch runtime.GOOS {
	case "linux", "darwin", "windows":
		if res.MemTotal == 0 || res.DiskTotal == 0 || res.DiskFree > res.DiskTotal || res.UptimeSec <= 0 || res.Kernel == "" {
			t.Fatalf("%s fields: %+v", runtime.GOOS, res)
		}
	}
	if runtime.GOOS != "windows" && len(res.Load) != 3 {
		t.Fatalf("load averages: %v", res.Load)
	}
	// The dispatcher knows every new type.
	for _, typ := range []protocol.TaskType{protocol.TaskStatus} {
		if _, err := Run(context.Background(), protocol.Task{Type: typ}, nil); err != nil {
			t.Errorf("Run(%s): %v", typ, err)
		}
	}
	for _, c := range []string{"udp", "tls", "dns_multi", "status", protocol.CapResolver} {
		found := false
		for _, have := range DetectCapabilities() {
			found = found || have == c
		}
		if !found {
			t.Errorf("capability %q not announced", c)
		}
	}
}
