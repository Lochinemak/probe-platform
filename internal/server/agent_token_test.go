package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"probe-platform/internal/protocol"
)

// tokenEnv is a server with a legacy shared token "shared", an admin session
// and a helper that connects as an agent.
type tokenEnv struct {
	t     *testing.T
	st    *Store
	hub   *Hub
	srv   *httptest.Server
	admin *http.Client
}

func newTokenEnv(t *testing.T) *tokenEnv {
	t.Helper()
	st, err := OpenStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := Config{AdminPassword: "pw", AdminUser: "admin", AgentToken: "shared", TaskTimeout: time.Minute, GuestAccess: true}
	hub := NewHub(cfg, st, nil, nil, discardLogger())
	srv := httptest.NewServer(NewHandler(cfg, hub, st, emptyFS{}, mustSettings(t, st, cfg), nil, nil, discardLogger()))
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	admin := &http.Client{Jar: jar}
	if resp, err := admin.Post(srv.URL+"/api/login", "application/json", strings.NewReader(`{"username":"admin","password":"pw"}`)); err != nil || resp.StatusCode != 200 {
		t.Fatalf("login: %v %v", err, resp)
	}
	return &tokenEnv{t: t, st: st, hub: hub, srv: srv, admin: admin}
}

func (e *tokenEnv) call(method, path, body string, out any) int {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.admin.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

// dial connects as an agent and returns the welcome, or the HTTP status /
// close reason that refused it.
func (e *tokenEnv) dial(token string, hello protocol.Hello) (*websocket.Conn, *protocol.Welcome, int, string) {
	e.t.Helper()
	url := "ws" + strings.TrimPrefix(e.srv.URL, "http") + "/ws/agent"
	conn, resp, err := websocket.DefaultDialer.Dial(url, http.Header{"Authorization": {"Bearer " + token}})
	if err != nil {
		if resp != nil {
			return nil, nil, resp.StatusCode, ""
		}
		e.t.Fatal(err)
	}
	if hello.Version == "" {
		hello.Version = "dev"
	}
	msg, _ := protocol.NewMessage(protocol.MsgHello, hello)
	if err := conn.WriteJSON(msg); err != nil {
		e.t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var m protocol.Message
	if err := conn.ReadJSON(&m); err != nil {
		conn.Close()
		return nil, nil, 0, err.Error()
	}
	var w protocol.Welcome
	_ = json.Unmarshal(m.Payload, &w)
	return conn, &w, 0, ""
}

// waitClosed reports whether the server dropped conn within a few seconds.
func waitClosed(conn *websocket.Conn) bool {
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return !strings.Contains(err.Error(), "timeout")
		}
	}
}

func (e *tokenEnv) waitOffline(id string) {
	e.t.Helper()
	for i := 0; i < 100; i++ {
		online := false
		for _, a := range e.hub.Agents() {
			if a.ID == id && a.Online {
				online = true
			}
		}
		if !online {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatalf("%s still online", id)
}

// TestNodeTokenIsIdentity: a node created on the dashboard is identified by
// its token, whatever name the agent reports, and reinstalling with the same
// token lands on the same node.
func TestNodeTokenIsIdentity(t *testing.T) {
	e := newTokenEnv(t)
	var created struct {
		Agent protocol.AgentStatus `json:"agent"`
		Token string               `json:"token"`
	}
	if code := e.call("POST", "/api/agents", `{"name":"home sz","location":"广东 深圳","isp":"电信"}`, &created); code != 201 || created.Token == "" || created.Agent.ID != "home-sz" {
		t.Fatalf("create: %d %+v", code, created)
	}
	if code := e.call("POST", "/api/agents", `{"name":"home sz"}`, nil); code != http.StatusConflict {
		t.Fatalf("duplicate name: %d", code)
	}
	if code := e.call("POST", "/api/agents", `{"name":"`+strings.Repeat("深", 22)+`"}`, nil); code != http.StatusBadRequest {
		t.Fatalf("over-long name: %d", code)
	}
	var list struct {
		Agents      []protocol.AgentStatus `json:"agents"`
		LegacyToken string                 `json:"legacy_token"`
	}
	e.call("GET", "/api/agents", "", &list)
	if len(list.Agents) != 1 || list.Agents[0].Auth != "" || list.Agents[0].Online || list.LegacyToken != "enabled" {
		t.Fatalf("pending node: %+v", list)
	}
	// Guests do not see nodes that never connected, nor the migration state.
	resp, err := http.Get(e.srv.URL + "/api/agents")
	if err != nil {
		t.Fatal(err)
	}
	var g map[string]json.RawMessage
	_ = json.NewDecoder(resp.Body).Decode(&g)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(g["agents"]) != "[]" || g["legacy_token"] != nil {
		t.Fatalf("guest view: %d %s", resp.StatusCode, g)
	}

	conn, w, code, reason := e.dial(created.Token, protocol.Hello{Name: "someone-else", TokenHandoff: true})
	if conn == nil {
		t.Fatalf("node token refused: %d %s", code, reason)
	}
	if w.AgentID != "home-sz" || w.Token != "" {
		t.Fatalf("welcome: %+v", w)
	}
	got, _ := e.st.GetAgent("home-sz")
	if got.Auth != "token" || got.Name != "someone-else" || got.Location != "广东 深圳" || got.FirstSeen.IsZero() {
		t.Fatalf("record after connect: %+v", got)
	}
	if _, err := e.st.GetAgent("someone-else"); err != ErrNotFound {
		t.Fatal("the reported name must not create a node")
	}
	// Reinstall elsewhere with the same token: same node, the old connection is replaced.
	conn2, w2, _, _ := e.dial(created.Token, protocol.Hello{Name: "home-sz", Location: "广东 广州"})
	if conn2 == nil || w2.AgentID != "home-sz" || !waitClosed(conn) {
		t.Fatalf("reinstall with the same token must take over the node: %+v", w2)
	}
	defer conn2.Close()
	if got, _ := e.st.GetAgent("home-sz"); got.Location != "广东 广州" || got.Name != "home-sz" {
		t.Fatalf("reinstall must overwrite the labels: %+v", got)
	}
	var tk map[string]string
	if e.call("GET", "/api/agents/home-sz/token", "", &tk); tk["token"] != created.Token {
		t.Fatalf("token lookup: %v", tk)
	}

	// Reset: the old token stops working and the live connection is dropped.
	if code := e.call("POST", "/api/agents/home-sz/token", "", &tk); code != 200 || tk["token"] == created.Token {
		t.Fatalf("reset: %d %v", code, tk)
	}
	if !waitClosed(conn2) {
		t.Fatal("reset must drop the connected agent")
	}
	if _, _, code, _ := e.dial(created.Token, protocol.Hello{Name: "home-sz"}); code != http.StatusUnauthorized {
		t.Fatalf("old token after reset: %d", code)
	}
	conn3, _, _, _ := e.dial(tk["token"], protocol.Hello{Name: "home-sz"})
	if conn3 == nil {
		t.Fatal("new token must work")
	}
	// Delete: the token is revoked with the node, even while it is online.
	if code := e.call("DELETE", "/api/agents/home-sz", "", nil); code != 200 {
		t.Fatalf("delete online node: %d", code)
	}
	if !waitClosed(conn3) {
		t.Fatal("delete must drop the connected agent")
	}
	if _, _, code, _ := e.dial(tk["token"], protocol.Hello{Name: "home-sz"}); code != http.StatusUnauthorized {
		t.Fatalf("token of a deleted node: %d", code)
	}
	time.Sleep(50 * time.Millisecond) // let the dropped session's cleanup run
	if _, err := e.st.GetAgent("home-sz"); err != ErrNotFound {
		t.Fatal("deleted node must stay deleted")
	}
}

// TestSharedTokenMigration covers nodes installed with the old shared token.
func TestSharedTokenMigration(t *testing.T) {
	e := newTokenEnv(t)
	for _, id := range []string{"old-a", "old-b"} {
		if err := e.st.UpsertAgent(&protocol.AgentStatus{ID: id, Name: id, Auth: "legacy"}); err != nil {
			t.Fatal(err)
		}
	}
	// Unknown names cannot use the shared token any more.
	if conn, _, _, reason := e.dial("shared", protocol.Hello{Name: "brand-new"}); conn != nil || !strings.Contains(reason, "existing nodes") {
		t.Fatalf("unknown node with shared token: %v", reason)
	}
	// An old agent keeps working as before and is not handed anything it cannot store.
	oldConn, w, _, reason := e.dial("shared", protocol.Hello{Name: "old-b"})
	if oldConn == nil || w.AgentID != "old-b" || w.Token != "" {
		t.Fatalf("old agent: %+v %s", w, reason)
	}
	defer oldConn.Close()
	// A new agent is handed its node's own token...
	conn, w, _, reason := e.dial("shared", protocol.Hello{Name: "old-a", TokenHandoff: true})
	if conn == nil || w.AgentID != "old-a" || w.Token == "" {
		t.Fatalf("handover: %+v %s", w, reason)
	}
	conn.Close()
	if got, _ := e.st.GetAgent("old-a"); got.Auth != "legacy" {
		t.Fatalf("not migrated until it connects with its own token: %+v", got)
	}
	var tk map[string]string
	if e.call("GET", "/api/agents/old-a/token", "", &tk); tk["token"] != w.Token {
		t.Fatalf("dashboard must show the handed-over token: %v vs %s", tk, w.Token)
	}
	// ...connects with it...
	conn, w2, _, _ := e.dial(w.Token, protocol.Hello{Name: "old-a", TokenHandoff: true})
	if conn == nil || w2.AgentID != "old-a" || w2.Token != "" {
		t.Fatalf("reconnect with own token: %+v", w2)
	}
	conn.Close()
	if got, _ := e.st.GetAgent("old-a"); got.Auth != "token" {
		t.Fatalf("migrated: %+v", got)
	}
	// ...after which the shared token no longer passes for that node.
	e.waitOffline("old-a")
	if conn, _, _, reason := e.dial("shared", protocol.Hello{Name: "old-a", TokenHandoff: true}); conn != nil || !strings.Contains(reason, "own token") {
		t.Fatalf("shared token for a migrated node: %v", reason)
	}

	// Switching the shared token off drops whoever still uses it.
	var view SettingsView
	if code := e.call("PUT", "/api/settings", `{"legacy_agent_token":false}`, &view); code != 200 || view.LegacyAgentToken != "disabled" {
		t.Fatalf("disable: %d %+v", code, view)
	}
	if !waitClosed(oldConn) {
		t.Fatal("disabling the shared token must drop its connections")
	}
	if _, _, code, _ := e.dial("shared", protocol.Hello{Name: "old-b"}); code != http.StatusUnauthorized {
		t.Fatalf("shared token after disable: %d", code)
	}
	req := httptest.NewRequest("GET", "/api/agent/version", nil)
	req.Header.Set("Authorization", "Bearer shared")
	if e.hub.checkToken(req) {
		t.Fatal("disabled shared token must not download binaries either")
	}
	e.call("PUT", "/api/settings", `{"legacy_agent_token":true}`, &view)
	if view.LegacyAgentToken != "enabled" {
		t.Fatalf("re-enable: %+v", view)
	}
}

// TestNoSharedToken: a fresh install has no shared token at all.
func TestNoSharedToken(t *testing.T) {
	st, _ := OpenStore(filepath.Join(t.TempDir(), "t.db"))
	defer st.Close()
	cfg := Config{AdminPassword: "pw", TaskTimeout: time.Minute}
	s := mustSettings(t, st, cfg)
	if s.LegacyTokenState() != "none" || s.LegacyAgentToken() != "" {
		t.Fatalf("state %q", s.LegacyTokenState())
	}
	if err := s.Update(SettingsPatch{LegacyAgentToken: ptr(true)}); err == nil {
		t.Fatal("enabling a shared token that does not exist must fail")
	}
	hub := NewHub(cfg, st, nil, nil, discardLogger())
	hub.SetLegacyToken(s.LegacyAgentToken)
	r := httptest.NewRequest("GET", "/ws/agent", nil)
	r.Header.Set("Authorization", "Bearer ")
	if hub.checkToken(r) {
		t.Fatal("an empty bearer must never authenticate")
	}
}

func ptr[T any](v T) *T { return &v }

// TestStoreMigratesAgentsToLegacy: nodes recorded before per-node tokens are
// marked legacy so they may keep using the shared token until migrated.
func TestStoreMigratesAgentsToLegacy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`CREATE TABLE agents (id TEXT PRIMARY KEY, name TEXT NOT NULL, location TEXT NOT NULL DEFAULT '', isp TEXT NOT NULL DEFAULT '',
		tags TEXT NOT NULL DEFAULT '[]', os TEXT NOT NULL DEFAULT '', arch TEXT NOT NULL DEFAULT '', version TEXT NOT NULL DEFAULT '',
		public_ip TEXT NOT NULL DEFAULT '', geo_location TEXT NOT NULL DEFAULT '', geo_isp TEXT NOT NULL DEFAULT '',
		capabilities TEXT NOT NULL DEFAULT '[]', first_seen INTEGER NOT NULL, last_seen INTEGER NOT NULL);
		INSERT INTO agents (id,name,first_seen,last_seen) VALUES ('szdebian','szdebian',1,2);`)
	raw.Close()
	if err != nil {
		t.Fatal(err)
	}
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a, err := st.GetAgent("szdebian")
	if err != nil || a.Auth != "legacy" {
		t.Fatalf("migrated agent: %+v %v", a, err)
	}
	tok, err := st.AgentToken("szdebian")
	if err != nil || len(tok) != 48 {
		t.Fatalf("token for a legacy node: %q %v", tok, err)
	}
	if again, _ := st.AgentToken("szdebian"); again != tok {
		t.Fatal("token must be stable once issued")
	}
	// Reopening must not touch migrated rows again.
	_ = st.UpsertAgent(&protocol.AgentStatus{ID: "szdebian", Name: "szdebian", Auth: "token"})
	st.Close()
	st, _ = OpenStore(path)
	if a, _ := st.GetAgent("szdebian"); a.Auth != "token" {
		t.Fatalf("second open re-marked the node: %+v", a)
	}
}
