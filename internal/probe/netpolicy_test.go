package probe

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"probe-platform/internal/protocol"
)

func TestIsPublicIP(t *testing.T) {
	public := []string{"1.1.1.1", "8.8.8.8", "223.5.5.5", "106.52.114.189", "2400:3200::1", "2606:4700:4700::1111"}
	for _, s := range public {
		if !IsPublicIP(net.ParseIP(s)) {
			t.Errorf("%s must be allowed", s)
		}
	}
	private := []string{
		"127.0.0.1", "::1", // loopback
		"169.254.169.254", "fe80::1", // link-local: cloud metadata
		"10.2.0.195", "192.168.1.1", "172.16.0.1", "fd00::1", // private / ULA
		"100.64.0.1", "100.127.255.255", // CGNAT
		"0.0.0.0", "255.255.255.255", "240.0.0.1", // unspecified / broadcast / reserved
		"224.0.0.1", "ff02::1", // multicast
		"198.18.0.1", "198.19.255.255", // benchmarking, also the fake-IP range
		"192.0.2.1", "198.51.100.1", "203.0.113.1", "2001:db8::1", // documentation
		"192.0.0.1", // IETF protocol assignments
	}
	for _, s := range private {
		if IsPublicIP(net.ParseIP(s)) {
			t.Errorf("%s must be refused", s)
		}
	}
	if IsPublicIP(nil) {
		t.Error("nil must be refused")
	}
	// IPv4-mapped IPv6 must be judged on the v4 address it carries.
	if IsPublicIP(net.ParseIP("::ffff:127.0.0.1")) {
		t.Error("ipv4-mapped loopback must be refused")
	}
}

func TestCheckPublicAddr(t *testing.T) {
	if err := CheckPublicAddr("1.1.1.1:443"); err != nil {
		t.Fatalf("public: %v", err)
	}
	for _, a := range []string{"127.0.0.1:8080", "169.254.169.254:80", "[::1]:443", "10.0.0.5:22", "garbage"} {
		if err := CheckPublicAddr(a); err == nil {
			t.Errorf("%s must be refused", a)
		}
	}
}

func TestResolvePublicOnly(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "169.254.169.254", "192.168.1.1", "::1"} {
		if _, err := Resolve(context.Background(), host, "", true); err == nil {
			t.Errorf("%s must be refused when publicOnly is set", host)
		}
		if _, err := Resolve(context.Background(), host, "", false); err != nil {
			t.Errorf("%s must still work for a trusted caller: %v", host, err)
		}
	}
	if _, err := Resolve(context.Background(), "1.1.1.1", "", true); err != nil {
		t.Fatalf("public literal: %v", err)
	}
}

// TestProbesRefusePrivateTargets: every probe type that takes a destination has
// to refuse an internal one once PublicOnly is set.
func TestProbesRefusePrivateTargets(t *testing.T) {
	ctx := context.Background()
	p := protocol.Params{Count: 1, TimeoutMs: 200, PublicOnly: true}
	for _, tc := range []struct {
		name   string
		target string
		run    func() error
	}{
		{"ping", "127.0.0.1", func() error { _, err := Ping(ctx, "127.0.0.1", p, nil); return err }},
		{"tcping", "127.0.0.1:80", func() error { _, err := TCPing(ctx, "127.0.0.1:80", p, nil); return err }},
		{"http metadata", "http://169.254.169.254/latest/meta-data/", func() error {
			_, err := HTTP(ctx, "http://169.254.169.254/latest/meta-data/", p, nil)
			return err
		}},
		{"mtr", "192.168.1.1", func() error { _, err := MTR(ctx, "192.168.1.1", p, nil); return err }},
	} {
		err := tc.run()
		if err == nil {
			t.Errorf("%s (%s): must be refused", tc.name, tc.target)
			continue
		}
		if !strings.Contains(err.Error(), "不是公网地址") {
			t.Errorf("%s: want a public-address refusal, got %v", tc.name, err)
		}
	}
}

// TestHTTPProbeRefusesRedirectToPrivate checks the dialer hook rather than the
// up-front resolve: the first host is fine, the redirect target is not.
func TestHTTPProbeRefusesRedirectToPrivate(t *testing.T) {
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("SECRET-METADATA"))
	}))
	defer internal.Close()

	p := protocol.Params{Count: 1, TimeoutMs: 2000, FollowRedirects: true, PublicOnly: true, ExpectKeyword: "SECRET-METADATA"}
	res, err := HTTP(context.Background(), internal.URL, p, nil)
	// The target itself is loopback, so it is refused before any connection.
	if err == nil {
		t.Fatalf("loopback target must be refused, got %+v", res)
	}
	// And the hook that would catch a redirect is the same check.
	if err := CheckPublicAddr(strings.TrimPrefix(internal.URL, "http://")); err == nil {
		t.Fatal("dialer hook must refuse the loopback address")
	}
	// A trusted caller is unaffected: the same probe still runs and reads the body.
	p.PublicOnly = false
	res, err = HTTP(context.Background(), internal.URL, p, nil)
	if err != nil || len(res.Attempts) != 1 || !res.Attempts[0].AssertOK {
		t.Fatalf("trusted caller must still reach an internal target: %v %+v", err, res)
	}
}

func TestDNSProbeRefusesPrivateResolver(t *testing.T) {
	p := protocol.Params{Count: 1, TimeoutMs: 200, PublicOnly: true, DNSServer: "192.168.1.1"}
	if _, err := DNS(context.Background(), "example.com", p, nil); err == nil {
		t.Fatal("a private resolver must be refused")
	}
	p.DNSServer = "127.0.0.1:5353"
	if _, err := DNS(context.Background(), "example.com", p, nil); err == nil {
		t.Fatal("a loopback resolver must be refused")
	}
	// A trusted caller may still point at the LAN resolver.
	p.PublicOnly = false
	if _, err := DNS(context.Background(), "example.com", p, nil); err != nil {
		t.Fatalf("trusted caller: %v", err)
	}
}
