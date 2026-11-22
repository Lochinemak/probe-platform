package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"probe-platform/internal/protocol"
)

// Canary strings, one per class of information a share can hide. Each is put
// in every field of its class; the test then checks which survive.
const (
	cTarget = "canary-target.example" // names the probed target
	cIP     = "203.0.113.77"          // an address the target resolved to, a hop, a resolver
	cRDNS   = "canary-rdns.example"   // reverse DNS of a hop: as telling as its address
	cNode   = "canary-node"           // node name, free-text location and ISP, city
	cID     = "canary-agentid"        // real node id: never shared
	cSecret = "canary-secret"         // owner, request headers and body: never shared
)

func canaryTask(typ protocol.TaskType) *protocol.Task {
	return &protocol.Task{ID: cSecret + "-taskid", Type: typ, Target: "https://" + cTarget + "/path", CreatedAt: time.Now(),
		AgentIDs: []string{cID + "-1"}, Owner: "guest:" + cSecret, MonitorID: cSecret,
		Params: protocol.Params{Count: 3, Port: 8443, Method: "POST", Headers: map[string]string{"Authorization": cSecret}, Body: cSecret,
			ExpectKeyword: cSecret, DNSServer: cIP + ":53", RecordType: "A", Protocol: "tcp"}}
}

func canaryResult(typ protocol.TaskType) *protocol.AgentResult {
	now := time.Now()
	boom := fmt.Sprintf("dial tcp %s:443 (%s): connection refused", cIP, cTarget)
	r := &protocol.AgentResult{TaskID: cSecret + "-taskid", AgentID: cID + "-1", AgentName: cNode + "-name", Location: cNode + "-location", ISP: cNode + "-isp",
		Geo:    &protocol.Geo{CC: "CN", Country: "中国", Province: "广东", City: cNode + "-city", Line: "电信", Src: "manual"},
		Status: protocol.StatusError, Error: boom, TargetIP: cIP, TargetGeo: "中国 浙江省", StartedAt: &now, FinishedAt: &now}
	prog := func(kind string, v any) {
		r.Progress = append(r.Progress, protocol.Progress{TaskID: r.TaskID, Seq: len(r.Progress), Kind: kind, Data: mustJSON(v), Geo: "中国 浙江省"})
	}
	prog("resolved", cIP)
	switch typ {
	case protocol.TaskPing, protocol.TaskTCPing:
		reply := protocol.Reply{Seq: 0, OK: false, Error: boom}
		r.Data = mustJSON(protocol.PingResult{Target: cTarget, IP: cIP, Port: 8443, Replies: []protocol.Reply{reply}})
		prog("reply", reply)
	case protocol.TaskHTTP:
		a := protocol.HTTPAttempt{Seq: 0, OK: true, Error: boom, URL: "https://" + cTarget + "/", FinalURL: "https://www." + cTarget + "/", IP: cIP, StatusCode: 200,
			Headers: map[string]string{"Server": cTarget, "Set-Cookie": "session=" + cSecret, "X-Auth-Token": cSecret}, Redirects: []string{"301 https://www." + cTarget + "/"},
			CertSubject: "CN=" + cTarget, CertIssuer: "Some CA", Assertions: []protocol.Assertion{{Name: "关键字", Pass: false, Detail: "正文不含 " + cTarget}}}
		r.Data = mustJSON(protocol.HTTPResult{URL: "https://" + cTarget + "/", Attempts: []protocol.HTTPAttempt{a}})
		prog("attempt", a)
	case protocol.TaskDNS:
		a := protocol.DNSAttempt{Seq: 0, OK: true, Error: boom, Server: cIP + ":53", Proto: "udp", RCode: "Success", Answers: []protocol.DNSAnswer{
			{Name: cTarget + ".", Type: "A", TTL: 60, Value: cIP, Geo: "中国 浙江省"},
			{Name: cTarget + ".", Type: "CNAME", TTL: 60, Value: "cdn." + cTarget + "."},
		}}
		r.Data = mustJSON(protocol.DNSResult{Target: cTarget, RecordType: "A", Attempts: []protocol.DNSAttempt{a}})
		prog("attempt", a)
	case protocol.TaskUDP:
		reply := protocol.Reply{Seq: 0, OK: false, State: "closed", Error: boom}
		r.Data = mustJSON(protocol.PingResult{Target: cTarget + ":53", IP: cIP, Port: 8443, Proto: "udp", Replies: []protocol.Reply{reply}})
		prog("reply", reply)
	case protocol.TaskTLS:
		a := protocol.TLSAttempt{Seq: 0, OK: false, Error: boom}
		r.Data = mustJSON(protocol.TLSResult{Target: cTarget, ServerName: cTarget, IP: cIP, Port: 8443, Attempts: []protocol.TLSAttempt{a},
			VerifyError: "x509: certificate is valid for other.example, not " + cTarget, DaysLeft: 30,
			Chain: []protocol.TLSCert{
				{Subject: "CN=" + cTarget, Issuer: "CN=Internal CA of " + cTarget, SANs: []string{cTarget, cIP}, Serial: "c0ffee"},
				{Subject: "CN=Internal CA of " + cTarget, Issuer: "CN=Public Root", IsCA: true},
			}})
		prog("attempt", a)
	case protocol.TaskDNSCompare:
		e := protocol.DNSCompareEntry{Server: "system", Label: "系统 DNS", Key: cIP, Attempt: protocol.DNSAttempt{OK: true, Error: boom, Server: cIP + ":53", Proto: "udp", RCode: "Success",
			Answers: []protocol.DNSAnswer{{Name: cTarget + ".", Type: "A", Value: cIP}, {Name: cTarget + ".", Type: "CNAME", Value: "cdn." + cTarget + "."}}}}
		r.Data = mustJSON(protocol.DNSCompareResult{Target: cTarget, RecordType: "A", Entries: []protocol.DNSCompareEntry{e}, Verdict: "consistent", Groups: 1})
		prog("entry", e)
	case protocol.TaskStatus:
		r.Data = mustJSON(protocol.StatusResult{Hostname: cSecret + "-host", Nameservers: []string{cIP}, DiskPath: "/volume1/" + cSecret})
	case protocol.TaskMTR:
		hops := []protocol.MTRHop{{TTL: 1, Hosts: []string{cIP}, Names: []string{cRDNS}, Geo: []string{"中国 广东省"}, Sent: 3, Received: 3, Reached: true}}
		r.Data = mustJSON(protocol.MTRResult{Target: cTarget, IP: cIP, Protocol: "tcp", Port: 8443, Hops: hops, Reached: true, Rounds: 3})
		prog("hops", hops)
	}
	return r
}

// TestShareRedactionCanary: for every probe type and every combination of
// the four switches, a share shows what it was told to show and not a thing
// more. A result field added later fails this test's "never" checks only if
// it is passed through unscrubbed, so the scrubbers are written as rebuilds:
// what they do not copy does not exist.
func TestShareRedactionCanary(t *testing.T) {
	// Every task type there is: one added without a scrubber fails here on
	// "what a share always carries" or, if it leaks, on the canaries.
	for _, typ := range protocol.TaskTypes {
		if typ == protocol.TaskStatus {
			continue // never shared; checked below
		}
		for mask := 0; mask < 16; mask++ {
			o := shareOpts{target: mask&1 != 0, time: mask&2 != 0, ips: mask&4 != 0, nodes: mask&8 != 0}
			in := &protocol.Event{Type: "snapshot", Task: canaryTask(typ), Results: []*protocol.AgentResult{canaryResult(typ)}, Done: true}
			out := string(mustJSON(o.scrubSnapshot(in)))
			check := func(canary string, want bool, what string) {
				t.Helper()
				if got := strings.Contains(out, canary); got != want {
					t.Errorf("%s %+v: %s present=%v, want %v", typ, o, what, got, want)
				}
			}
			check(cSecret, false, "owner / request headers / body")
			check(cID, false, "real node id")
			check(cTarget, o.target, "target")
			check(cIP, o.ips, "addresses")
			if typ == protocol.TaskMTR {
				check(cRDNS, o.ips, "hop reverse DNS")
			}
			check(cNode, o.nodes, "node name / location")
			check(`"created_at":"0001`, !o.time, "task time hidden")
			check(`"started_at"`, o.time, "result times")
			// What a share always carries.
			check(`"province":"广东"`, true, "province")
			check(`"line":"电信"`, true, "line")
			check(`"agent_id":"n1"`, true, "node alias")
			check(`"src"`, false, "how the node's place was worked out")
			// The failure itself survives as a kind even when its text cannot.
			check("connection refused", true, "error kind")
		}
	}
	// A type the scrubbers do not know yields no payload at all.
	o := shareOpts{target: true, time: true, ips: true, nodes: true}
	unknown := &protocol.AgentResult{AgentID: "a", Status: protocol.StatusDone, Data: json.RawMessage(`{"anything":"` + cSecret + `"}`),
		Progress: []protocol.Progress{{Kind: "attempt", Data: json.RawMessage(`{"x":"` + cSecret + `"}`)}}}
	if got := string(mustJSON(o.scrubResult(protocol.TaskType("future"), unknown, "n1"))); strings.Contains(got, cSecret) {
		t.Errorf("unknown task type passed through: %s", got)
	}
	// A node's own status is not something to share: the payload is dropped.
	st := canaryResult(protocol.TaskStatus)
	if got := string(mustJSON(o.scrubResult(protocol.TaskStatus, st, "n1"))); strings.Contains(got, "hostname") || strings.Contains(got, cSecret) {
		t.Errorf("status payload shared: %s", got)
	}
	// A mangled payload is dropped, not echoed.
	if got := o.scrubData(protocol.TaskPing, json.RawMessage(`"`+cSecret+`"`)); got != nil {
		t.Errorf("unparseable payload passed through: %s", got)
	}
}

// shareEnv is a server with guest access switched off: only a logged-in
// administrator can use the dashboard, and share links must still open.
func newShareEnv(t *testing.T, guest bool) (srv *httptest.Server, st *Store, hub *Hub, admin *http.Client) {
	t.Helper()
	st, err := OpenStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := Config{AdminPassword: "pw", AdminUser: "admin", AgentToken: "tok", TaskTimeout: time.Minute, GuestAccess: guest}
	hub = NewHub(cfg, st, nil, nil, discardLogger())
	srv = httptest.NewServer(NewHandler(cfg, hub, st, emptyFS{}, mustSettings(t, st, cfg), nil, nil, discardLogger()))
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	admin = &http.Client{Jar: jar}
	if resp, err := admin.Post(srv.URL+"/api/login", "application/json", strings.NewReader(`{"username":"admin","password":"pw"}`)); err != nil || resp.StatusCode != 200 {
		t.Fatalf("login: %v %v", err, resp)
	}
	return srv, st, hub, admin
}

func doJSON(t *testing.T, c *http.Client, method, url, body string, out any) int {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

type sharedPage struct {
	Share   publicShare             `json:"share"`
	Task    protocol.Task           `json:"task"`
	Results []*protocol.AgentResult `json:"results"`
	Done    bool                    `json:"done"`
}

// TestShareLifecycle: create, open without any session while guests are shut
// out, count visits, cap the number of links, expire, revoke, and outlive the
// task's own retention.
func TestShareLifecycle(t *testing.T) {
	srv, st, _, admin := newShareEnv(t, false)
	task := canaryTask(protocol.TaskPing)
	task.ID = "t-share"
	if err := st.InsertTask(task); err != nil {
		t.Fatal(err)
	}
	res := canaryResult(protocol.TaskPing)
	res.TaskID, res.Status, res.Error = task.ID, protocol.StatusDone, ""
	if err := st.UpsertResult(res); err != nil {
		t.Fatal(err)
	}
	anon := &http.Client{}
	if code := doJSON(t, anon, "GET", srv.URL+"/api/tasks/"+task.ID, "", nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous task read with guests off: %d", code)
	}
	if code := doJSON(t, anon, "POST", srv.URL+"/api/tasks/"+task.ID+"/shares", `{}`, nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous share creation with guests off: %d", code)
	}
	for _, bad := range []string{`{"mode":"forever"}`, `{"expires":"1y"}`} {
		if code := doJSON(t, admin, "POST", srv.URL+"/api/tasks/"+task.ID+"/shares", bad, nil); code != http.StatusBadRequest {
			t.Errorf("POST %s: %d", bad, code)
		}
	}
	if code := doJSON(t, admin, "POST", srv.URL+"/api/tasks/nope/shares", `{}`, nil); code != http.StatusNotFound {
		t.Errorf("share of an unknown task: %d", code)
	}

	var created struct {
		Share shareView `json:"share"`
	}
	if code := doJSON(t, admin, "POST", srv.URL+"/api/tasks/"+task.ID+"/shares", `{"expires":"24h","show_target":true}`, &created); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	sh := created.Share
	if len(sh.Token) != 32 || sh.Mode != shareModeFollow || !sh.ShowTarget || !sh.ShowTime || sh.ShowIPs || sh.ShowNodes || !sh.Active {
		t.Fatalf("created share: %+v", sh)
	}
	if d := time.Until(sh.ExpiresAt); d < 23*time.Hour || d > 25*time.Hour {
		t.Fatalf("expiry: %v", d)
	}

	// Anyone with the link reads it: no cookie, guests off.
	var page sharedPage
	resp, err := anon.Get(srv.URL + "/api/shares/" + sh.Token)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("open share: %v %v", err, resp)
	}
	if resp.Header.Get("Cache-Control") != "no-store" || !strings.Contains(resp.Header.Get("X-Robots-Tag"), "noindex") {
		t.Errorf("share headers: %v", resp.Header)
	}
	_ = json.NewDecoder(resp.Body).Decode(&page)
	resp.Body.Close()
	if !page.Done || page.Share.Live || len(page.Results) != 1 || !strings.Contains(page.Task.Target, cTarget) || page.Task.Owner != "" || page.Task.ID != "" {
		t.Fatalf("shared page: %+v task %+v", page.Share, page.Task)
	}
	if r := page.Results[0]; r.AgentID != "n1" || r.AgentName != "节点 1" || r.TargetIP != "" || r.Location != "" {
		t.Fatalf("shared result: %+v", r)
	}
	doJSON(t, anon, "GET", srv.URL+"/api/shares/"+sh.Token, "", nil)
	var list struct {
		Shares []shareView `json:"shares"`
	}
	doJSON(t, admin, "GET", srv.URL+"/api/tasks/"+task.ID+"/shares", "", &list)
	if len(list.Shares) != 1 || list.Shares[0].Visits != 2 {
		t.Fatalf("list after two visits: %+v", list.Shares)
	}
	if code := doJSON(t, anon, "GET", srv.URL+"/api/shares/"+strings.Repeat("0", 32), "", nil); code != http.StatusNotFound {
		t.Errorf("unknown token: %d", code)
	}

	// It outlives the task: the frozen result is stored with the share.
	if _, err := st.PruneTasks(time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if code := doJSON(t, anon, "GET", srv.URL+"/api/shares/"+sh.Token, "", &page); code != 200 || len(page.Results) != 1 {
		t.Fatalf("share after its task was pruned: %d", code)
	}

	// Expired links stop opening.
	if _, err := st.db.Exec(`UPDATE shares SET expires_at=? WHERE token=?`, unixMs(time.Now().Add(-time.Minute)), sh.Token); err != nil {
		t.Fatal(err)
	}
	if code := doJSON(t, anon, "GET", srv.URL+"/api/shares/"+sh.Token, "", nil); code != http.StatusNotFound {
		t.Errorf("expired share: %d", code)
	}
	if n, err := st.PruneShares(time.Now()); err != nil || n != 1 {
		t.Errorf("prune expired shares: %d %v", n, err)
	}
}

func TestShareLimitAndRevoke(t *testing.T) {
	srv, st, _, admin := newShareEnv(t, true)
	task := &protocol.Task{ID: "t-lim", Type: protocol.TaskPing, Target: "example.com", CreatedAt: time.Now(), Owner: "admin:admin"}
	_ = st.InsertTask(task)
	var tokens []string
	for i := 0; i < maxSharesPerTask; i++ {
		var c struct {
			Share shareView `json:"share"`
		}
		if code := doJSON(t, admin, "POST", srv.URL+"/api/tasks/t-lim/shares", `{"mode":"snapshot"}`, &c); code != http.StatusCreated {
			t.Fatalf("share %d: %d", i, code)
		}
		tokens = append(tokens, c.Share.Token)
	}
	if code := doJSON(t, admin, "POST", srv.URL+"/api/tasks/t-lim/shares", `{}`, nil); code != http.StatusConflict {
		t.Fatalf("one share too many: %d", code)
	}
	// A guest can neither see nor revoke the administrator's shares.
	jar, _ := cookiejar.New(nil)
	guest := &http.Client{Jar: jar}
	if code := doJSON(t, guest, "DELETE", srv.URL+"/api/shares/"+tokens[0], "", nil); code != http.StatusNotFound {
		t.Errorf("guest revoking an admin's share: %d", code)
	}
	if code := doJSON(t, guest, "GET", srv.URL+"/api/tasks/t-lim/shares", "", nil); code != http.StatusNotFound {
		t.Errorf("guest listing shares of someone else's task: %d", code)
	}
	if code := doJSON(t, admin, "DELETE", srv.URL+"/api/shares/"+tokens[0], "", nil); code != 200 {
		t.Fatalf("revoke: %d", code)
	}
	if code := doJSON(t, guest, "GET", srv.URL+"/api/shares/"+tokens[0], "", nil); code != http.StatusNotFound {
		t.Errorf("revoked share still opens: %d", code)
	}
	if code := doJSON(t, guest, "GET", srv.URL+"/api/shares/"+tokens[1], "", nil); code != 200 {
		t.Errorf("another share of the same task: %d", code)
	}
	// Revoking made room for a new one.
	if code := doJSON(t, admin, "POST", srv.URL+"/api/tasks/t-lim/shares", `{}`, nil); code != http.StatusCreated {
		t.Errorf("share after revoking one: %d", code)
	}
}

// TestGuestCannotShareOthersTask: a guest shares only what their own browser
// started, and manages only their own links.
func TestGuestCannotShareOthersTask(t *testing.T) {
	srv, _, _, _ := newShareEnv(t, true)
	guestLimiter.mu.Lock()
	guestLimiter.attempts = map[string][]time.Time{}
	guestLimiter.mu.Unlock()
	newGuest := func() *http.Client { jar, _ := cookiejar.New(nil); return &http.Client{Jar: jar} }
	alice, bob := newGuest(), newGuest()
	var snap protocol.Event
	if code := doJSON(t, alice, "POST", srv.URL+"/api/tasks", `{"type":"ping","target":"1.1.1.1","agent_ids":["ghost"]}`, &snap); code != http.StatusCreated {
		t.Fatalf("alice's task: %d", code)
	}
	id := snap.Task.ID
	if code := doJSON(t, bob, "POST", srv.URL+"/api/tasks/"+id+"/shares", `{}`, nil); code != http.StatusNotFound {
		t.Fatalf("bob sharing alice's task: %d", code)
	}
	var c struct {
		Share shareView `json:"share"`
	}
	if code := doJSON(t, alice, "POST", srv.URL+"/api/tasks/"+id+"/shares", `{"show_target":true}`, &c); code != http.StatusCreated {
		t.Fatalf("alice sharing her own task: %d", code)
	}
	if code := doJSON(t, bob, "DELETE", srv.URL+"/api/shares/"+c.Share.Token, "", nil); code != http.StatusNotFound {
		t.Errorf("bob revoking alice's share: %d", code)
	}
	// Bob can open it, as anyone with the link can.
	var page sharedPage
	if code := doJSON(t, bob, "GET", srv.URL+"/api/shares/"+c.Share.Token, "", &page); code != 200 || page.Task.Target != "1.1.1.1" {
		t.Errorf("bob opening alice's share: %d %+v", code, page.Task)
	}
	if code := doJSON(t, alice, "DELETE", srv.URL+"/api/shares/"+c.Share.Token, "", nil); code != 200 {
		t.Errorf("alice revoking her share: %d", code)
	}
}

// TestShareFollowsRunningTask: a "follow" share of a running task streams
// scrubbed events and is frozen when the task ends.
func TestShareFollowsRunningTask(t *testing.T) {
	e := newTokenEnv(t)
	var created struct {
		Token string `json:"token"`
	}
	e.call("POST", "/api/agents", `{"name":"real-node-name","location":"广东 深圳"}`, &created)
	conn, _, _, _ := e.dial(created.Token, protocol.Hello{Name: "real-node-name"})
	if conn == nil {
		t.Fatal("dial")
	}
	defer conn.Close()
	var snap protocol.Event
	if code := e.call("POST", "/api/tasks", `{"type":"ping","target":"secret-target.example","params":{"count":3}}`, &snap); code != 201 {
		t.Fatalf("task: %d", code)
	}
	id := snap.Task.ID
	readMsg(t, conn, protocol.MsgTask)
	var c struct {
		Share shareView `json:"share"`
	}
	if code := e.call("POST", "/api/tasks/"+id+"/shares", `{}`, &c); code != 201 {
		t.Fatalf("share: %d", code)
	}
	var page sharedPage
	if code := doJSON(t, http.DefaultClient, "GET", e.srv.URL+"/api/shares/"+c.Share.Token, "", &page); code != 200 || !page.Share.Live || page.Done {
		t.Fatalf("live share: %d %+v done=%v", code, page.Share, page.Done)
	}

	resp, err := http.Get(e.srv.URL + "/api/shares/" + c.Share.Token + "/events")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("events: %v %v", err, resp)
	}
	defer resp.Body.Close()
	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			if l := sc.Text(); strings.HasPrefix(l, "data: ") {
				lines <- strings.TrimPrefix(l, "data: ")
			}
		}
		close(lines)
	}()
	next := func() string {
		t.Helper()
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatal("stream ended early")
			}
			return l
		case <-time.After(5 * time.Second):
			t.Fatal("no event")
		}
		return ""
	}
	if first := next(); !strings.Contains(first, `"type":"snapshot"`) || strings.Contains(first, "secret-target") || strings.Contains(first, "real-node-name") {
		t.Fatalf("snapshot event: %s", first)
	}
	send := func(typ string, v any) {
		m, _ := protocol.NewMessage(typ, v)
		if err := conn.WriteJSON(m); err != nil {
			t.Fatal(err)
		}
	}
	send(protocol.MsgProgress, protocol.Progress{TaskID: id, Seq: 1, Kind: "resolved", Data: mustJSON("198.51.100.9")})
	send(protocol.MsgProgress, protocol.Progress{TaskID: id, Seq: 2, Kind: "reply", Data: mustJSON(protocol.Reply{Seq: 0, OK: true, RTTMs: 12})})
	send(protocol.MsgResult, protocol.Result{TaskID: id, OK: true, Data: mustJSON(protocol.PingResult{Target: "secret-target.example", IP: "198.51.100.9",
		Replies: []protocol.Reply{{Seq: 0, OK: true, RTTMs: 12}}, Stats: protocol.Stats{Sent: 1, Received: 1, AvgMs: 12}})})
	var got []string
	for {
		l := next()
		got = append(got, l)
		if strings.Contains(l, `"type":"done"`) {
			break
		}
	}
	all := strings.Join(got, "\n")
	for _, leak := range []string{"secret-target", "198.51.100.9", "real-node-name", "real-node"} {
		if strings.Contains(all, leak) {
			t.Errorf("stream leaked %q:\n%s", leak, all)
		}
	}
	for _, want := range []string{`"type":"progress"`, `"type":"result"`, `"agent_id":"n1"`, `"rtt_ms":12`} {
		if !strings.Contains(all, want) {
			t.Errorf("stream lacks %s:\n%s", want, all)
		}
	}
	// The done hook freezes the share; from then on it is served from its own copy.
	deadline := time.Now().Add(3 * time.Second)
	for {
		s, err := e.st.GetShare(c.Share.Token)
		if err == nil && len(s.Snapshot) > 0 {
			if strings.Contains(string(s.Snapshot), "secret-target") || !strings.Contains(string(s.Snapshot), `"avg_ms":12`) {
				t.Fatalf("frozen snapshot: %s", s.Snapshot)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("share was not frozen when its task finished")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if code := doJSON(t, http.DefaultClient, "GET", e.srv.URL+"/api/shares/"+c.Share.Token, "", &page); code != 200 || page.Share.Live || !page.Done {
		t.Fatalf("share after the task finished: %d live=%v done=%v", code, page.Share.Live, page.Done)
	}
}

func TestErrorKind(t *testing.T) {
	for msg, want := range map[string]string{
		"":                                  "",
		"stopped":                           "stopped",
		"cancelled":                         "cancelled",
		"agent offline":                     "agent offline",
		"timeout":                           "timeout",
		"dial tcp 1.2.3.4:443: i/o timeout": "dial tcp: i/o timeout",
		"dial tcp 1.2.3.4:443: connect: connection refused":          "connection refused",
		"lookup secret.example: no such host":                        "no such host",
		"tls: failed to verify certificate: x509: …":                 "tls: handshake failure",
		"read tcp 10.0.0.2:5->1.2.3.4:443: connection reset by peer": "connection reset",
		"something with secret.example in it":                        "failed",
	} {
		if got := errorKind(msg); got != want {
			t.Errorf("errorKind(%q) = %q, want %q", msg, got, want)
		}
	}
}
