package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"probe-platform/internal/protocol"
)

func TestSplitRegion(t *testing.T) {
	cases := map[string][2]string{
		"中国|0|广东省|深圳市|电信":  {"中国 广东省 深圳市", "电信"},
		"中国|广东省|深圳市|电信":    {"中国 广东省 深圳市", "电信"},
		"中国|0|北京|北京市|0":    {"中国 北京 北京市", ""},
		"0|0|0|内网IP|内网IP":  {"内网IP", "内网IP"},
		"美国|0|加利福尼亚|洛杉矶|0": {"美国 加利福尼亚 洛杉矶", ""},
		// v3 layout with trailing country code
		"中国|广东省|广州市|电信|CN":                         {"中国 广东省 广州市", "电信"},
		"中国|江苏省|南京市|0|CN":                          {"中国 江苏省 南京市", ""},
		"中国|0|0|移动|CN":                             {"中国", "移动"},
		"中国|北京市|北京市|联通|CN":                         {"中国 北京市", "联通"},
		"United States|California|0|Google LLC|US": {"United States California", "Google LLC"},
	}
	for in, want := range cases {
		loc, isp := splitRegion(in)
		if loc != want[0] || isp != want[1] {
			t.Errorf("%q: got (%q,%q) want (%q,%q)", in, loc, isp, want[0], want[1])
		}
	}
}

func TestNormalizeISP(t *testing.T) {
	if got := normalizeISP("Chinanet Guangdong Province Network"); got != "电信" {
		t.Fatalf("got %q", got)
	}
	if got := normalizeISP("China Unicom Beijing"); got != "联通" {
		t.Fatalf("got %q", got)
	}
	if got := normalizeISP("Some Small ISP"); got != "Some Small ISP" {
		t.Fatalf("got %q", got)
	}
}

func TestAgentIDFromName(t *testing.T) {
	if got := agentIDFromName("  home sz / nas "); got != "home-sz---nas" {
		t.Fatalf("got %q", got)
	}
	if got := agentIDFromName("深圳家里"); got != "深圳家里" {
		t.Fatalf("unicode names must be kept: %q", got)
	}
	if got := agentIDFromName("   "); got != "" {
		t.Fatalf("blank must be empty: %q", got)
	}
}

func TestSessionAuth(t *testing.T) {
	a := newSessionAuth(Config{AdminPassword: "secret", AdminUser: "root", GuestAccess: true})
	tok, _ := a.issue(Session{Role: roleAdmin, Name: "root", Via: "password"})
	sess, ok := a.parse(tok)
	if !ok || sess.Role != roleAdmin || sess.Name != "root" {
		t.Fatalf("fresh token must validate: %+v %v", sess, ok)
	}
	if _, ok := a.parse(tok + "x"); ok {
		t.Fatal("tampered token must fail")
	}
	other := newSessionAuth(Config{AdminPassword: "different"})
	if _, ok := other.parse(tok); ok {
		t.Fatal("token from another password must fail")
	}
	if !a.checkPassword("root", "secret") || a.checkPassword("admin", "secret") || a.checkPassword("root", "nope") {
		t.Fatal("password check must require the configured username and password")
	}
	for i := 0; i < 10; i++ {
		if !a.allowAttempt("1.2.3.4") {
			t.Fatalf("attempt %d should be allowed", i)
		}
	}
	if a.allowAttempt("1.2.3.4") {
		t.Fatal("11th attempt must be rate limited")
	}
	if !a.allowAttempt("5.6.7.8") {
		t.Fatal("other ip unaffected")
	}
	anon := httptest.NewRequest("GET", "/", nil)
	if a.role(anon) != roleGuest {
		t.Fatal("anonymous visitor is a guest when guest access is on")
	}
	strict := newSessionAuth(Config{AdminPassword: "secret", GuestAccess: false})
	if strict.role(anon) != "" {
		t.Fatal("guest access off means anonymous must log in")
	}
	open := newSessionAuth(Config{})
	if open.role(anon) != roleAdmin {
		t.Fatal("no admin login configured means open mode")
	}
	withCookie := httptest.NewRequest("GET", "/", nil)
	withCookie.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	if a.role(withCookie) != roleAdmin {
		t.Fatal("valid cookie must be admin")
	}
}

func TestStoreRoundTrip(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	a := &protocol.AgentStatus{ID: "a1", Name: "a1", Location: "gd", ISP: "ct", Tags: []string{"x"}, Capabilities: []string{"tcp"}}
	if err := st.UpsertAgent(a); err != nil {
		t.Fatal(err)
	}
	first := a.FirstSeen
	a.GeoLocation = "中国 广东"
	time.Sleep(2 * time.Millisecond)
	a.LastSeen = time.Now()
	if err := st.UpsertAgent(a); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetAgent("a1")
	if err != nil {
		t.Fatal(err)
	}
	if got.GeoLocation != "中国 广东" || got.Tags[0] != "x" || got.FirstSeen.UnixMilli() != first.UnixMilli() {
		t.Fatalf("agent: %+v", got)
	}
	// A later upsert with empty geo must not wipe the stored geo.
	if err := st.UpsertAgent(&protocol.AgentStatus{ID: "a1", Name: "a1"}); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetAgent("a1")
	if got.GeoLocation != "中国 广东" {
		t.Fatalf("geo wiped: %+v", got)
	}
	if _, err := st.GetAgent("missing"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}

	task := &protocol.Task{ID: "t1", Type: protocol.TaskPing, Target: "1.1.1.1", Params: protocol.Params{Count: 3}, CreatedAt: time.Now(), AgentIDs: []string{"a1"}}
	if err := st.InsertTask(task); err != nil {
		t.Fatal(err)
	}
	res := &protocol.AgentResult{TaskID: "t1", AgentID: "a1", AgentName: "a1", Status: protocol.StatusDone, Data: json.RawMessage(`{"ip":"1.1.1.1"}`)}
	now := time.Now()
	res.FinishedAt = &now
	if err := st.UpsertResult(res); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListResults("t1")
	if err != nil || len(list) != 1 || list[0].Status != protocol.StatusDone || string(list[0].Data) != `{"ip":"1.1.1.1"}` {
		t.Fatalf("results: %v %+v", err, list)
	}
	tasks, err := st.ListTasks(10, 0, "")
	if err != nil || len(tasks) != 1 || tasks[0].Params.Count != 3 {
		t.Fatalf("tasks: %v %+v", err, tasks)
	}
	n, err := st.PruneTasks(time.Now().Add(time.Minute))
	if err != nil || n != 1 {
		t.Fatalf("prune: %v %d", err, n)
	}
	if left, _ := st.ListResults("t1"); len(left) != 0 {
		t.Fatal("results must be pruned with their task")
	}
}

func TestAPIAuthGate(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := Config{AdminPassword: "pw", AdminUser: "admin", AgentToken: "tok", TaskTimeout: time.Minute, GuestAccess: true}
	hub := NewHub(cfg, st, nil, nil, discardLogger())
	h := NewHandler(cfg, hub, st, emptyFS{}, nil, nil, discardLogger())
	srv := httptest.NewServer(h)
	defer srv.Close()

	// Guests may list agents (trimmed) but not touch admin endpoints.
	resp, _ := http.Get(srv.URL + "/api/agents")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("guest agents: %d", resp.StatusCode)
	}
	for _, p := range []string{"/api/monitors", "/api/notify", "/api/agent/token", "/api/alerts"} {
		if resp, _ := http.Get(srv.URL + p); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("guest must not access %s: %d", p, resp.StatusCode)
		}
	}
	resp, _ = http.Get(srv.URL + "/ws/agent")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("agent ws without token: %d", resp.StatusCode)
	}
	resp, _ = http.Get(srv.URL + "/api/session")
	var sess map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&sess)
	if sess["auth_required"] != true || sess["authenticated"] != false || sess["role"] != "guest" {
		t.Fatalf("session: %v", sess)
	}
	// Wrong username is rejected even with the right password.
	resp, _ = http.Post(srv.URL+"/api/login", "application/json", strings.NewReader(`{"username":"root","password":"pw"}`))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong user: %d", resp.StatusCode)
	}

	// Guest access off: everything needs a login.
	strictCfg := cfg
	strictCfg.GuestAccess = false
	strictSrv := httptest.NewServer(NewHandler(strictCfg, hub, st, emptyFS{}, nil, nil, discardLogger()))
	defer strictSrv.Close()
	if resp, _ := http.Get(strictSrv.URL + "/api/agents"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("strict guest agents: %d", resp.StatusCode)
	}
}

func TestPublicAgentTrimsDetails(t *testing.T) {
	ag := &protocol.AgentStatus{ID: "a", Name: "home", Online: true, PublicIP: "1.2.3.4", OS: "linux", Arch: "amd64", Version: "v1", GeoLocation: "中国 广东", GeoISP: "电信", Capabilities: []string{"mtr"}}
	p := publicAgent(ag)
	if p.PublicIP != "" || p.OS != "" || p.Version != "" || p.Location != "中国 广东" || p.ISP != "电信" || len(p.Capabilities) != 1 {
		t.Fatalf("trimmed: %+v", p)
	}
	gp := guestParams(protocol.TaskHTTP, protocol.Params{Count: 20, SpeedTest: true})
	if gp.Count != 3 || gp.SpeedTest {
		t.Fatalf("guest http params: %+v", gp)
	}
}
