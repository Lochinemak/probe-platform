package server

import (
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"testing"
	"time"
)

// TestIPLookupKinds: private and fake-IP addresses are recognised without any
// database, and nothing breaks when there is none.
func TestIPLookupKinds(t *testing.T) {
	var none *GeoIP
	for ip, want := range map[string]IPInfo{
		"192.168.1.1":  {IP: "192.168.1.1", Version: 4, Kind: "private"},
		"127.0.0.1":    {IP: "127.0.0.1", Version: 4, Kind: "private"},
		"100.64.1.1":   {IP: "100.64.1.1", Version: 4, Kind: "private"},
		"198.18.0.77":  {IP: "198.18.0.77", Version: 4, Kind: "fake_ip"},
		"fe80::1":      {IP: "fe80::1", Version: 6, Kind: "private"},
		"223.5.5.5":    {IP: "223.5.5.5", Version: 4, Kind: "public"},
		"2400:3200::1": {IP: "2400:3200::1", Version: 6, Kind: "public"},
	} {
		if got := none.LookupInfo(ip, true); got != want {
			t.Errorf("%s: %+v, want %+v", ip, got, want)
		}
	}
	path := os.Getenv("PROBE_TEST_XDB")
	if path == "" {
		t.Skip("PROBE_TEST_XDB not set: database lookups not tested")
	}
	g := NewGeoIP(Config{IP2RegionDB: path, DataDir: t.TempDir()}, discardLogger())
	info := g.LookupInfo("223.5.5.5", false)
	if info.Source != "offline" || info.Geo == nil || info.Geo.CC != "CN" || info.Location == "" || info.ASN != "" {
		t.Fatalf("offline lookup: %+v geo %+v", info, info.Geo)
	}
	// Online lookups are off in this GeoIP: asking for one changes nothing.
	if again := g.LookupInfo("223.5.5.5", true); again.Source != "offline" {
		t.Fatalf("online lookup happened though it is disabled: %+v", again)
	}
}

func TestIPLookupAPI(t *testing.T) {
	old := ipLookupLimiter
	ipLookupLimiter = newRateLimiter(3, time.Minute)
	t.Cleanup(func() { ipLookupLimiter = old })
	srv, _, _, admin := newShareEnv(t, true)
	jar, _ := cookiejar.New(nil)
	guest := &http.Client{Jar: jar}

	var me IPInfo
	if code := doJSON(t, guest, "GET", srv.URL+"/api/ip/me", "", &me); code != 200 || me.IP != "127.0.0.1" || me.Kind != "private" {
		t.Fatalf("me: %d %+v", code, me)
	}
	for _, bad := range []string{"", "not-an-ip", "1.2.3", "example.com"} {
		if code := doJSON(t, guest, "GET", srv.URL+"/api/ip/lookup?ip="+bad, "", nil); code != http.StatusBadRequest {
			t.Errorf("lookup %q: %d", bad, code)
		}
	}
	var info IPInfo
	if code := doJSON(t, guest, "GET", srv.URL+"/api/ip/lookup?ip=[2400:3200::1]", "", &info); code != 200 || info.Version != 6 || info.Kind != "public" {
		t.Fatalf("bracketed v6: %d %+v", code, info)
	}
	// Three a minute for a guest in this test; the fourth is refused.
	doJSON(t, guest, "GET", srv.URL+"/api/ip/lookup?ip=10.0.0.1", "", nil)
	doJSON(t, guest, "GET", srv.URL+"/api/ip/lookup?ip=10.0.0.2", "", nil)
	if code := doJSON(t, guest, "GET", srv.URL+"/api/ip/lookup?ip=10.0.0.3", "", nil); code != http.StatusTooManyRequests {
		t.Fatalf("guest over the limit: %d", code)
	}
	// Administrators are not limited.
	for i := 0; i < 6; i++ {
		if code := doJSON(t, admin, "GET", srv.URL+"/api/ip/lookup?ip=10.0.0.9", "", nil); code != 200 {
			t.Fatalf("admin lookup %d: %d", i, code)
		}
	}
	// The page that calls the two overseas "what is my IP" services may only
	// call those.
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "connect-src 'self' https://www.cloudflare.com https://api.ipify.org;") || strings.Contains(csp, "connect-src *") {
		t.Errorf("connect-src: %s", csp)
	}
}
