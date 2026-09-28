package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"probe-platform/internal/protocol"
)

// TestClientIPIgnoresClientSuppliedHops: X-Forwarded-For is a list the caller
// starts, so only the entries our own proxy appended may be trusted. Taking the
// leftmost one would let anyone pick their own identity and sail past every
// rate limit.
func TestClientIPIgnoresClientSuppliedHops(t *testing.T) {
	req := func(xff, xreal string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "203.0.113.9:1234"
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		if xreal != "" {
			r.Header.Set("X-Real-IP", xreal)
		}
		return r
	}
	// Not behind a proxy: headers are ignored entirely.
	if got := clientIP(req("1.2.3.4", "1.2.3.4"), false, 1); got != "203.0.113.9" {
		t.Fatalf("untrusted proxy: %q", got)
	}
	// One proxy: it appended the peer address last, and that is the only entry
	// we may believe. "1.2.3.4" is what the caller claimed.
	if got := clientIP(req("1.2.3.4, 198.51.100.7", ""), true, 1); got != "198.51.100.7" {
		t.Fatalf("spoofed leading entry was trusted: %q", got)
	}
	// A caller sending several entries cannot push the real one out of reach.
	if got := clientIP(req("9.9.9.9, 8.8.8.8, 1.1.1.1, 198.51.100.7", ""), true, 1); got != "198.51.100.7" {
		t.Fatalf("multi-entry spoof: %q", got)
	}
	// No header at all: the proxy's own entry is the only one.
	if got := clientIP(req("198.51.100.7", ""), true, 1); got != "198.51.100.7" {
		t.Fatalf("single entry: %q", got)
	}
	// Two proxies in front.
	if got := clientIP(req("1.2.3.4, 198.51.100.7, 10.0.0.2", ""), true, 2); got != "198.51.100.7" {
		t.Fatalf("two hops: %q", got)
	}
	// Fewer entries than configured hops, or junk: fall back to X-Real-IP, then
	// to the peer address. Never to a caller-chosen value.
	if got := clientIP(req("1.2.3.4", "198.51.100.7"), true, 2); got != "198.51.100.7" {
		t.Fatalf("short chain must fall back: %q", got)
	}
	if got := clientIP(req("not-an-ip", ""), true, 1); got != "203.0.113.9" {
		t.Fatalf("junk header: %q", got)
	}
	if got := (Config{TrustProxy: true}).ClientIP(req("1.2.3.4, 198.51.100.7", "")); got != "198.51.100.7" {
		t.Fatalf("Config.ClientIP defaults to one hop: %q", got)
	}
}

// TestRateLimiterSweep: the table must not keep an entry per address forever.
func TestRateLimiterSweep(t *testing.T) {
	a := &sessionAuth{attempts: map[string][]time.Time{}}
	for i := 0; i < 500; i++ {
		a.allowAttempt(net4(i))
	}
	if len(a.attempts) != 500 {
		t.Fatalf("fresh entries must be kept: %d", len(a.attempts))
	}
	// Age everything past the window and force a sweep.
	stale := time.Now().Add(-2 * time.Minute)
	for ip := range a.attempts {
		a.attempts[ip] = []time.Time{stale}
	}
	a.lastSweep = stale
	a.allowAttempt("203.0.113.1")
	if len(a.attempts) != 1 {
		t.Fatalf("stale entries must be swept, %d left", len(a.attempts))
	}
	// Sweeping must not forgive a caller inside the window.
	for i := 0; i < 10; i++ {
		a.allowAttempt("203.0.113.2")
	}
	if a.allowAttempt("203.0.113.2") {
		t.Fatal("limit must still apply after a sweep")
	}
}

func net4(i int) string {
	return "198.51." + itoa(i/256) + "." + itoa(i%256)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestGuestParamsAreAPolicyNotADefault(t *testing.T) {
	// Everything a guest could use to turn an agent into a request forge.
	in := protocol.Params{
		Count: 99, SpeedTest: true, SpeedSeconds: 60, InsecureTLS: true, PublicOnly: false,
		Method: "POST", Body: "id=1", Headers: map[string]string{"Authorization": "Bearer x", "Host": "internal"},
	}
	got := guestParams(protocol.TaskHTTP, in)
	if !got.PublicOnly {
		t.Error("public_only must be forced on")
	}
	if got.InsecureTLS {
		t.Error("insecure_tls must be forced off")
	}
	if got.Method != "GET" || got.Body != "" || got.Headers != nil {
		t.Errorf("method/body/headers must be stripped: %+v", got)
	}
	if got.Count != 3 || got.SpeedTest || got.SpeedSeconds != 0 {
		t.Errorf("limits: %+v", got)
	}
	if got := guestParams(protocol.TaskHTTP, protocol.Params{Method: "head"}); got.Method != "HEAD" {
		t.Errorf("HEAD stays allowed: %q", got.Method)
	}
	// Other types are pinned to the public internet as well.
	for _, typ := range []protocol.TaskType{protocol.TaskPing, protocol.TaskTCPing, protocol.TaskMTR, protocol.TaskDNS} {
		if !guestParams(typ, protocol.Params{}).PublicOnly {
			t.Errorf("%s: public_only must be forced on", typ)
		}
	}
}

func TestValidateGuestTarget(t *testing.T) {
	ctx := context.Background()
	refuse := []struct {
		typ    protocol.TaskType
		target string
		params protocol.Params
	}{
		{protocol.TaskPing, "127.0.0.1", protocol.Params{}},
		{protocol.TaskPing, "169.254.169.254", protocol.Params{}},                           // cloud metadata
		{protocol.TaskHTTP, "http://169.254.169.254/latest/meta-data/", protocol.Params{}},  // the real prize
		{protocol.TaskHTTP, "http://metadata.tencentyun.com/", protocol.Params{}},           // resolves privately
		{protocol.TaskHTTP, "192.168.1.1", protocol.Params{}},                               // scheme added, still private
		{protocol.TaskTCPing, "10.2.0.195:22", protocol.Params{}},                           // LAN port scan
		{protocol.TaskTCPing, "[::1]:8080", protocol.Params{}},                              //
		{protocol.TaskMTR, "100.64.0.1", protocol.Params{}},                                 // CGNAT
		{protocol.TaskDNS, "example.com", protocol.Params{DNSServer: "192.168.1.1"}},        // LAN resolver
		{protocol.TaskDNS, "example.com", protocol.Params{DNSServer: "1.1.1.1:8080"}},       // port prober
		{protocol.TaskDNS, "example.com", protocol.Params{DNSServer: "http://1.1.1.1/dns"}}, // plaintext DoH
	}
	for _, tc := range refuse {
		if err := validateGuestTarget(ctx, tc.typ, tc.target, tc.params); err == nil {
			t.Errorf("%s %s %+v: must be refused", tc.typ, tc.target, tc.params)
		}
	}
	// Literal addresses keep this independent of whatever the machine running
	// the tests resolves names to: a host behind a fake-IP transparent proxy
	// sees 198.18.x.x for every domain.
	allow := []struct {
		typ    protocol.TaskType
		target string
		params protocol.Params
	}{
		{protocol.TaskPing, "1.1.1.1", protocol.Params{}},
		{protocol.TaskHTTP, "https://1.1.1.1/", protocol.Params{}},
		{protocol.TaskHTTP, "223.5.5.5", protocol.Params{}},
		{protocol.TaskTCPing, "223.5.5.5:443", protocol.Params{}},
		{protocol.TaskMTR, "8.8.8.8", protocol.Params{}},
		{protocol.TaskDNS, "anything.example", protocol.Params{}}, // node's own resolver: no destination to check
		{protocol.TaskDNS, "anything.example", protocol.Params{DNSServer: "223.5.5.5"}},
		{protocol.TaskDNS, "anything.example", protocol.Params{DNSServer: "223.5.5.5:53"}},
	}
	for _, tc := range allow {
		if err := validateGuestTarget(ctx, tc.typ, tc.target, tc.params); err != nil {
			t.Errorf("%s %s: must be allowed, got %v", tc.typ, tc.target, err)
		}
	}
}

// TestGuestCannotProbeInternalTargets drives the real endpoint: an anonymous
// visitor must not be able to aim an agent at the dashboard host's own metadata
// service or at a home LAN.
func TestGuestCannotProbeInternalTargets(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := Config{AdminPassword: "pw", AdminUser: "admin", AgentToken: "tok", TaskTimeout: time.Minute, GuestAccess: true}
	hub := NewHub(cfg, st, nil, nil, discardLogger())
	srv := httptest.NewServer(NewHandler(cfg, hub, st, emptyFS{}, mustSettings(t, st, cfg), nil, nil, discardLogger()))
	defer srv.Close()

	post := func(c *http.Client, body string) (int, string) {
		resp, err := c.Post(srv.URL+"/api/tasks", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		msg, _ := out["error"].(string)
		return resp.StatusCode, msg
	}
	guest := &http.Client{}
	for _, body := range []string{
		`{"type":"http","target":"http://169.254.169.254/latest/meta-data/","params":{"expect_keyword":"AccessKey"}}`,
		`{"type":"http","target":"http://127.0.0.1:8080/api/settings"}`,
		`{"type":"tcping","target":"10.2.0.195:22"}`,
		`{"type":"ping","target":"192.168.1.1"}`,
		`{"type":"dns","target":"x.com","params":{"dns_server":"192.168.1.1"}}`,
	} {
		code, msg := post(guest, body)
		if code != http.StatusBadRequest || !strings.Contains(msg, "不是公网地址") && !strings.Contains(msg, "端口") {
			t.Errorf("%s -> %d %q, want a refusal", body, code, msg)
		}
	}
	// A public target gets past the policy and fails only for want of an agent.
	if code, msg := post(guest, `{"type":"ping","target":"1.1.1.1"}`); code != http.StatusBadRequest || !strings.Contains(msg, "no agents online") {
		t.Fatalf("public target: %d %q", code, msg)
	}

	// An administrator is still allowed to probe their own LAN.
	jar, _ := cookiejar.New(nil)
	admin := &http.Client{Jar: jar}
	if resp, _ := admin.Post(srv.URL+"/api/login", "application/json", strings.NewReader(`{"username":"admin","password":"pw"}`)); resp.StatusCode != 200 {
		t.Fatal("admin login failed")
	}
	if code, msg := post(admin, `{"type":"tcping","target":"10.2.0.195:22"}`); code != http.StatusBadRequest || !strings.Contains(msg, "no agents online") {
		t.Fatalf("admin LAN probe must not be blocked by the guest policy: %d %q", code, msg)
	}
}

// TestAgentTokenNotAcceptedInQuery: a token in the URL ends up in every proxy
// access log, so only the Authorization header is honoured.
func TestAgentTokenNotAcceptedInQuery(t *testing.T) {
	st, _ := OpenStore(filepath.Join(t.TempDir(), "t.db"))
	defer st.Close()
	cfg := Config{AgentToken: "tok", AdminPassword: "pw", TaskTimeout: time.Minute}
	hub := NewHub(cfg, st, nil, nil, discardLogger())
	srv := httptest.NewServer(NewHandler(cfg, hub, st, emptyFS{}, mustSettings(t, st, cfg), nil, nil, discardLogger()))
	defer srv.Close()
	if resp, _ := http.Get(srv.URL + "/ws/agent?token=tok"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("query token must not authenticate: %d", resp.StatusCode)
	}
	r := httptest.NewRequest("GET", "/ws/agent?token=tok", nil)
	if hub.checkToken(r) {
		t.Fatal("checkToken must ignore the query string")
	}
	r = httptest.NewRequest("GET", "/ws/agent", nil)
	r.Header.Set("Authorization", "Bearer tok")
	if !hub.checkToken(r) {
		t.Fatal("bearer header must authenticate")
	}
	r.Header.Set("Authorization", "Bearer wrong")
	if hub.checkToken(r) {
		t.Fatal("wrong token must fail")
	}
	// An empty configured token must never match an empty header.
	empty := NewHub(Config{}, st, nil, nil, discardLogger())
	r.Header.Set("Authorization", "Bearer ")
	if empty.checkToken(r) {
		t.Fatal("unset token must refuse everything")
	}
}

func TestCrossSiteGuard(t *testing.T) {
	st, _ := OpenStore(filepath.Join(t.TempDir(), "t.db"))
	defer st.Close()
	cfg := Config{AdminPassword: "pw", AdminUser: "admin", AgentToken: "tok", TaskTimeout: time.Minute, GuestAccess: true}
	hub := NewHub(cfg, st, nil, nil, discardLogger())
	srv := httptest.NewServer(NewHandler(cfg, hub, st, emptyFS{}, mustSettings(t, st, cfg), nil, nil, discardLogger()))
	defer srv.Close()

	do := func(method, path string, headers map[string]string) int {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	for _, h := range []map[string]string{
		{"Sec-Fetch-Site": "cross-site"},
		{"Sec-Fetch-Site": "same-site"},
		{"Origin": "https://evil.example.com"},
	} {
		if code := do("POST", "/api/login", h); code != http.StatusForbidden {
			t.Errorf("%v must be refused, got %d", h, code)
		}
	}
	// Same-origin requests and non-browser clients pass through to the handler.
	for _, h := range []map[string]string{
		{"Sec-Fetch-Site": "same-origin"},
		{"Sec-Fetch-Site": "none"},
		{},
	} {
		if code := do("POST", "/api/login", h); code == http.StatusForbidden {
			t.Errorf("%v must be allowed", h)
		}
	}
	// Reads are never blocked: a cross-site GET cannot change anything.
	if code := do("GET", "/api/session", map[string]string{"Sec-Fetch-Site": "cross-site"}); code != http.StatusOK {
		t.Errorf("cross-site GET: %d", code)
	}
}

func TestSecurityHeadersAndVersionGating(t *testing.T) {
	st, _ := OpenStore(filepath.Join(t.TempDir(), "t.db"))
	defer st.Close()
	cfg := Config{AdminPassword: "pw", AdminUser: "admin", AgentToken: "tok", TaskTimeout: time.Minute, GuestAccess: true, AgentImage: "img:latest"}
	hub := NewHub(cfg, st, nil, nil, discardLogger())
	srv := httptest.NewServer(NewHandler(cfg, hub, st, emptyFS{}, mustSettings(t, st, cfg), nil, nil, discardLogger()))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	h := resp.Header
	if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("X-Frame-Options") != "DENY" || h.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("missing hardening headers: %v", h)
	}
	if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("csp: %q", csp)
	}

	// The build is an administrator's business only.
	body := func(c *http.Client, path string) map[string]any {
		resp, err := c.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return out
	}
	guest := &http.Client{}
	if v := body(guest, "/api/session"); v["version"] != nil || v["agent_image"] != nil {
		t.Fatalf("guest session must not carry the version: %v", v)
	}
	if v := body(guest, "/api/health"); v["ok"] != true || v["version"] != nil {
		t.Fatalf("guest health: %v", v)
	}
	jar, _ := cookiejar.New(nil)
	admin := &http.Client{Jar: jar}
	if resp, _ := admin.Post(srv.URL+"/api/login", "application/json", strings.NewReader(`{"username":"admin","password":"pw"}`)); resp.StatusCode != 200 {
		t.Fatal("login failed")
	}
	if v := body(admin, "/api/session"); v["agent_image"] != "img:latest" {
		t.Fatalf("admin session must keep the details: %v", v)
	}
}

// TestChannelSecretsNeverLeaveTheServer: bot tokens and SMTP passwords are
// write-only over the API, and saving a form that does not carry them back must
// not wipe them.
func TestChannelSecretsNeverLeaveTheServer(t *testing.T) {
	st, _ := OpenStore(filepath.Join(t.TempDir(), "t.db"))
	defer st.Close()
	cfg := Config{AdminPassword: "pw", AdminUser: "admin", AgentToken: "tok", TaskTimeout: time.Minute}
	hub := NewHub(cfg, st, nil, nil, discardLogger())
	srv := httptest.NewServer(NewHandler(cfg, hub, st, emptyFS{}, mustSettings(t, st, cfg), nil, NewNotifier(discardLogger()), discardLogger()))
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if resp, _ := c.Post(srv.URL+"/api/login", "application/json", strings.NewReader(`{"username":"admin","password":"pw"}`)); resp.StatusCode != 200 {
		t.Fatal("login failed")
	}

	const botToken = "123456:SUPER-SECRET-BOT-TOKEN"
	resp, err := c.Post(srv.URL+"/api/notify", "application/json",
		strings.NewReader(`{"name":"tg","type":"telegram","enabled":true,"config":{"bot_token":"`+botToken+`","chat_id":"42"}}`))
	if err != nil {
		t.Fatal(err)
	}
	var created protocol.NotifyChannel
	_ = json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if created.ID == "" {
		t.Fatal("no channel created")
	}
	if created.Config["bot_token"] != "" || !created.SecretSet["bot_token"] {
		t.Fatalf("create response leaked the token: %+v", created)
	}

	raw := func(path string) string {
		resp, err := c.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b := make([]byte, 8192)
		n, _ := resp.Body.Read(b)
		return string(b[:n])
	}
	if list := raw("/api/notify"); strings.Contains(list, botToken) {
		t.Fatalf("list leaked the token: %s", list)
	} else if !strings.Contains(list, `"secret_set"`) || !strings.Contains(list, `"chat_id":"42"`) {
		t.Fatalf("list must still describe the channel: %s", list)
	}

	// Saving the form as the dashboard sees it (no token) keeps the stored one.
	req, _ := http.NewRequest("PUT", srv.URL+"/api/notify/"+created.ID, strings.NewReader(
		`{"name":"tg renamed","type":"telegram","enabled":true,"config":{"bot_token":"","chat_id":"43"}}`))
	req.Header.Set("Content-Type", "application/json")
	if resp, err := c.Do(req); err != nil || resp.StatusCode != 200 {
		t.Fatalf("update: %v %v", err, resp.StatusCode)
	}
	stored, err := st.GetChannel(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Config["bot_token"] != botToken {
		t.Fatalf("an empty field must keep the stored credential, got %q", stored.Config["bot_token"])
	}
	if stored.Config["chat_id"] != "43" || stored.Name != "tg renamed" {
		t.Fatalf("the rest of the update must apply: %+v", stored)
	}
	// A new value replaces it.
	req, _ = http.NewRequest("PUT", srv.URL+"/api/notify/"+created.ID, strings.NewReader(
		`{"name":"tg","type":"telegram","enabled":true,"config":{"bot_token":"NEW-TOKEN","chat_id":"43"}}`))
	req.Header.Set("Content-Type", "application/json")
	if _, err := c.Do(req); err != nil {
		t.Fatal(err)
	}
	stored, _ = st.GetChannel(created.ID)
	if stored.Config["bot_token"] != "NEW-TOKEN" {
		t.Fatalf("new credential must be saved: %q", stored.Config["bot_token"])
	}
}

// TestLogtoAdminAllowlistIsRequired: an empty allowlist must not mean "every
// account on that provider is an administrator here".
func TestLogtoAdminAllowlistIsRequired(t *testing.T) {
	base := Config{LogtoEndpoint: "https://auth.example.com", LogtoAppID: "app", BaseURL: "https://probe.example.com"}
	l := NewLogto(base, discardLogger())
	if l == nil {
		t.Fatal("logto must be configured")
	}
	if l.HasAdmins() {
		t.Fatal("no allowlist was given")
	}
	if l.IsAdmin(&Identity{Sub: "u1", Email: "anyone@example.com"}) {
		t.Fatal("an empty allowlist must grant nobody")
	}
	withList := base
	withList.LogtoAdmins = " Me@Example.com , u9 "
	l = NewLogto(withList, discardLogger())
	if !l.HasAdmins() {
		t.Fatal("allowlist not parsed")
	}
	if !l.IsAdmin(&Identity{Sub: "x", Email: "me@example.com"}) {
		t.Fatal("listed email must be admin (case-insensitive)")
	}
	if !l.IsAdmin(&Identity{Sub: "u9"}) {
		t.Fatal("listed sub must be admin")
	}
	if l.IsAdmin(&Identity{Sub: "u8", Email: "other@example.com"}) {
		t.Fatal("unlisted account must not be admin")
	}
}

// TestCheckPublicHostResolution covers the branches that depend on what a name
// resolves to, with a stub resolver so the result does not change with the
// machine running the tests.
func TestCheckPublicHostResolution(t *testing.T) {
	orig := lookupIP
	t.Cleanup(func() { lookupIP = orig })
	stub := func(ips ...string) {
		lookupIP = func(context.Context, string, string) ([]net.IP, error) {
			out := make([]net.IP, 0, len(ips))
			for _, s := range ips {
				out = append(out, net.ParseIP(s))
			}
			return out, nil
		}
	}

	stub("1.1.1.1", "2606:4700::1111")
	if err := checkPublicHost(context.Background(), "public.example"); err != nil {
		t.Fatalf("all-public answer: %v", err)
	}

	// One internal answer among several is enough to refuse: otherwise a name
	// with a mixed answer set would be a coin flip.
	stub("1.1.1.1", "127.0.0.1")
	if err := checkPublicHost(context.Background(), "rebind.example"); err == nil {
		t.Fatal("a mixed answer must be refused")
	}

	stub("169.254.169.254")
	err := checkPublicHost(context.Background(), "metadata.example")
	if err == nil || !strings.Contains(err.Error(), "不是公网地址") {
		t.Fatalf("metadata answer: %v", err)
	}

	// A fake-IP answer is this server's DNS being hijacked, not a hostile
	// target, and says so.
	stub("198.18.5.132")
	err = checkPublicHost(context.Background(), "proxied.example")
	if err == nil || !strings.Contains(err.Error(), "fake-IP") {
		t.Fatalf("fake-ip answer: %v", err)
	}

	// A lookup this server cannot complete is left to the agent, which enforces
	// the same policy on the address it connects to.
	lookupIP = func(context.Context, string, string) ([]net.IP, error) {
		return nil, errors.New("server dns is down")
	}
	if err := checkPublicHost(context.Background(), "unknown.example"); err != nil {
		t.Fatalf("unresolvable name must be left to the agent: %v", err)
	}
}
