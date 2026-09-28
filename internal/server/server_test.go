package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
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

func mustSettings(t *testing.T, st *Store, cfg Config) *Settings {
	t.Helper()
	s, err := LoadSettings(st, cfg, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSessionAuth(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := newSessionAuth(mustSettings(t, st, Config{AdminPassword: "secret", AdminUser: "root", GuestAccess: true}))
	tok, _ := a.issue(Session{Role: roleAdmin, Name: "root", Via: "password"})
	sess, ok := a.parse(tok)
	if !ok || sess.Role != roleAdmin || sess.Name != "root" {
		t.Fatalf("fresh token must validate: %+v %v", sess, ok)
	}
	if _, ok := a.parse(tok + "x"); ok {
		t.Fatal("tampered token must fail")
	}
	st2, _ := OpenStore(filepath.Join(t.TempDir(), "t2.db"))
	defer st2.Close()
	other := newSessionAuth(mustSettings(t, st2, Config{AdminPassword: "different"}))
	if _, ok := other.parse(tok); ok {
		t.Fatal("token from another install must fail")
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
	strict := newSessionAuth(mustSettings(t, st2, Config{AdminPassword: "secret", GuestAccess: false}))
	if strict.role(anon) != "" {
		t.Fatal("guest access off means anonymous must log in")
	}
	st3, _ := OpenStore(filepath.Join(t.TempDir(), "t3.db"))
	defer st3.Close()
	open := newSessionAuth(mustSettings(t, st3, Config{}))
	if open.role(anon) != roleAdmin {
		t.Fatal("no admin login configured means open mode")
	}
	withCookie := httptest.NewRequest("GET", "/", nil)
	withCookie.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	if a.role(withCookie) != roleAdmin {
		t.Fatal("valid cookie must be admin")
	}
	// Dashboard-set password replaces the env one and logs existing sessions out.
	if err := a.settings.SetPassword("newpassword"); err != nil {
		t.Fatal(err)
	}
	if a.checkPassword("root", "secret") || !a.checkPassword("root", "newpassword") {
		t.Fatal("new password must replace the env password")
	}
	if a.role(withCookie) != roleGuest {
		t.Fatal("old session must be invalid after a password change")
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
	tasks, err := st.ListTasks(10, 0, TaskFilter{})
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
	h := NewHandler(cfg, hub, st, emptyFS{}, mustSettings(t, st, cfg), nil, nil, discardLogger())
	srv := httptest.NewServer(h)
	defer srv.Close()

	// Guests may list agents (trimmed) but not touch admin endpoints.
	resp, _ := http.Get(srv.URL + "/api/agents")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("guest agents: %d", resp.StatusCode)
	}
	for _, p := range []string{"/api/monitors", "/api/notify", "/api/agents/a1/token", "/api/alerts"} {
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
	st2, _ := OpenStore(filepath.Join(t.TempDir(), "strict.db"))
	defer st2.Close()
	strictSrv := httptest.NewServer(NewHandler(strictCfg, hub, st2, emptyFS{}, mustSettings(t, st2, strictCfg), nil, nil, discardLogger()))
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

// TestMigrateTasksOwner opens a database created by the first release (tasks
// without monitor_id/owner) and checks both columns get added.
func TestMigrateTasksOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`CREATE TABLE tasks (id TEXT PRIMARY KEY, type TEXT NOT NULL, target TEXT NOT NULL, params TEXT NOT NULL DEFAULT '{}', agent_ids TEXT NOT NULL DEFAULT '[]', created_at INTEGER NOT NULL);
		INSERT INTO tasks (id,type,target,created_at) VALUES ('old1','ping','1.1.1.1',1)`)
	if err != nil {
		t.Fatal(err)
	}
	raw.Close()

	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	old, err := st.GetTask("old1")
	if err != nil || old.Owner != "" || old.MonitorID != "" {
		t.Fatalf("old row after migration: %v %+v", err, old)
	}
	if err := st.InsertTask(&protocol.Task{ID: "g1", Type: protocol.TaskPing, Target: "1.1.1.1", CreatedAt: time.Now(), Owner: "guest:abc"}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertTask(&protocol.Task{ID: "a1", Type: protocol.TaskPing, Target: "1.1.1.1", CreatedAt: time.Now(), Owner: "admin:root"}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertTask(&protocol.Task{ID: "m1", Type: protocol.TaskPing, Target: "1.1.1.1", CreatedAt: time.Now(), MonitorID: "mon"}); err != nil {
		t.Fatal(err)
	}
	all, _ := st.ListTasks(10, 0, TaskFilter{MonitorID: "*"})
	if len(all) != 4 {
		t.Fatalf("all: %d", len(all))
	}
	adhoc, _ := st.ListTasks(10, 0, TaskFilter{})
	if len(adhoc) != 3 {
		t.Fatalf("ad-hoc: %d", len(adhoc))
	}
	mine, _ := st.ListTasks(10, 0, TaskFilter{Owner: "guest:abc"})
	if len(mine) != 1 || mine[0].ID != "g1" || mine[0].Owner != "guest:abc" {
		t.Fatalf("guest filter: %+v", mine)
	}
	// Reopening must be a no-op (columns already there).
	st.Close()
	if st2, err := OpenStore(path); err != nil {
		t.Fatal(err)
	} else {
		st2.Close()
	}
}

func TestGuestOwnerCookie(t *testing.T) {
	for _, v := range []string{"", "short", strings.Repeat("g", 32), strings.Repeat("A", 32), "../" + strings.Repeat("a", 29)} {
		r := httptest.NewRequest("GET", "/", nil)
		if v != "" {
			r.AddCookie(&http.Cookie{Name: guestCookie, Value: v})
		}
		if guestIDFrom(r) != "" {
			t.Fatalf("accepted bad guest id %q", v)
		}
	}
	good := strings.Repeat("0f", 16)
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: guestCookie, Value: good})
	if guestIDFrom(r) != good {
		t.Fatal("rejected valid guest id")
	}
	// Open mode: everyone is the configured admin user.
	st, _ := OpenStore(filepath.Join(t.TempDir(), "t.db"))
	defer st.Close()
	a := newSessionAuth(mustSettings(t, st, Config{AdminUser: "root"}))
	if got := a.owner(r); got != "admin:root" {
		t.Fatalf("open-mode owner: %q", got)
	}
}

// TestGuestHistoryIsolation: guests only list and open what their own browser
// started; administrators see everything.
func TestGuestHistoryIsolation(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := Config{AdminPassword: "pw", AdminUser: "admin", AgentToken: "tok", TaskTimeout: time.Minute, GuestAccess: true}
	hub := NewHub(cfg, st, nil, nil, discardLogger())
	srv := httptest.NewServer(NewHandler(cfg, hub, st, emptyFS{}, mustSettings(t, st, cfg), nil, nil, discardLogger()))
	defer srv.Close()

	client := func() *http.Client {
		jar, _ := cookiejar.New(nil)
		return &http.Client{Jar: jar}
	}
	get := func(c *http.Client, path string) (int, map[string]any) {
		resp, err := c.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return resp.StatusCode, body
	}
	post := func(c *http.Client, path, body string) (int, map[string]any) {
		resp, err := c.Post(srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	taskIDs := func(body map[string]any) []string {
		var ids []string
		for _, x := range body["tasks"].([]any) {
			ids = append(ids, x.(map[string]any)["id"].(string))
		}
		return ids
	}
	// The agent is offline, so the task finishes immediately with errors —
	// enough to exercise ownership without a WebSocket.
	const probe = `{"type":"ping","target":"1.1.1.1","agent_ids":["a1"]}`

	guestA, guestB, admin := client(), client(), client()
	code, snap := post(guestA, "/api/tasks", probe)
	if code != http.StatusCreated {
		t.Fatalf("guest create: %d %v", code, snap)
	}
	taskA := snap["task"].(map[string]any)["id"].(string)
	if owner, _ := snap["task"].(map[string]any)["owner"].(string); !strings.HasPrefix(owner, "guest:") {
		t.Fatalf("guest task owner: %q", owner)
	}
	u, _ := url.Parse(srv.URL)
	var guestID string
	for _, c := range guestA.Jar.Cookies(u) {
		if c.Name == guestCookie {
			guestID = c.Value
		}
	}
	if len(guestID) != 32 {
		t.Fatalf("guest cookie not set: %q", guestID)
	}

	// Guest A sees their task in the list and can open it.
	if code, body := get(guestA, "/api/tasks"); code != 200 || len(taskIDs(body)) != 1 || taskIDs(body)[0] != taskA {
		t.Fatalf("guest A list: %d %v", code, body)
	}
	if code, _ := get(guestA, "/api/tasks/"+taskA); code != 200 {
		t.Fatalf("guest A get own: %d", code)
	}
	// Asking for monitor runs must not widen a guest's view.
	if code, body := get(guestA, "/api/tasks?monitor=*"); code != 200 || len(taskIDs(body)) != 1 {
		t.Fatalf("guest A list monitor=*: %d %v", code, body)
	}

	// Guest B (no cookie yet) sees nothing and cannot open A's task by id.
	if code, body := get(guestB, "/api/tasks"); code != 200 || len(taskIDs(body)) != 0 {
		t.Fatalf("guest B list: %d %v", code, body)
	}
	for _, p := range []string{"/api/tasks/" + taskA, "/api/tasks/" + taskA + "/events"} {
		if code, _ := get(guestB, p); code != http.StatusNotFound {
			t.Fatalf("guest B %s: %d", p, code)
		}
	}
	if code, _ := post(guestB, "/api/tasks/"+taskA+"/cancel", ""); code != http.StatusNotFound {
		t.Fatalf("guest B cancel: %d", code)
	}
	// Guest B starts their own; still only sees that one.
	code, snap = post(guestB, "/api/tasks", probe)
	if code != http.StatusCreated {
		t.Fatalf("guest B create: %d", code)
	}
	taskB := snap["task"].(map[string]any)["id"].(string)
	if code, body := get(guestB, "/api/tasks"); code != 200 || len(taskIDs(body)) != 1 || taskIDs(body)[0] != taskB {
		t.Fatalf("guest B list after create: %d %v", code, body)
	}
	if code, _ := get(guestB, "/api/tasks/"+taskA); code != http.StatusNotFound {
		t.Fatalf("guest B get A's task: %d", code)
	}
	// A second task from guest A reuses the same cookie.
	post(guestA, "/api/tasks", probe)
	if code, body := get(guestA, "/api/tasks"); code != 200 || len(taskIDs(body)) != 2 {
		t.Fatalf("guest A list after second: %d %v", code, body)
	}

	// Admin sees all three and can open a guest's task.
	if code, _ := post(admin, "/api/login", `{"username":"admin","password":"pw"}`); code != 200 {
		t.Fatalf("login: %d", code)
	}
	if code, body := get(admin, "/api/tasks"); code != 200 || len(taskIDs(body)) != 3 {
		t.Fatalf("admin list: %d %v", code, body)
	}
	if code, _ := get(admin, "/api/tasks/"+taskA); code != 200 {
		t.Fatalf("admin get guest task: %d", code)
	}
	code, snap = post(admin, "/api/tasks", probe)
	if code != http.StatusCreated || snap["task"].(map[string]any)["owner"] != "admin:admin" {
		t.Fatalf("admin create: %d %v", code, snap)
	}
	if _, body := get(admin, "/api/tasks"); len(taskIDs(body)) != 4 {
		t.Fatalf("admin list after own: %v", body)
	}
	if _, body := get(guestA, "/api/tasks"); len(taskIDs(body)) != 2 {
		t.Fatalf("guest A must not see admin task: %v", body)
	}
}
