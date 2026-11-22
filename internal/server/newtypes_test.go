package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"probe-platform/internal/protocol"
)

func TestMissingCap(t *testing.T) {
	newer := []string{"tcp", "http", "udp", "tls", "dns_multi", "status", protocol.CapResolver}
	older := []string{"tcp", "http", "icmp"}
	for _, c := range []struct {
		name string
		have []string
		typ  protocol.TaskType
		p    protocol.Params
		want string
	}{
		{"the original probes need nothing", nil, protocol.TaskPing, protocol.Params{}, ""},
		{"old agent, new type", older, protocol.TaskUDP, protocol.Params{}, "udp"},
		{"old agent, tls", older, protocol.TaskTLS, protocol.Params{}, "tls"},
		{"old agent, dns_compare", older, protocol.TaskDNSCompare, protocol.Params{}, "dns_multi"},
		{"old agent, status", nil, protocol.TaskStatus, protocol.Params{}, "status"},
		{"old agent, old type, new parameter", older, protocol.TaskHTTP, protocol.Params{ConnectTo: "1.2.3.4"}, protocol.CapResolver},
		{"old agent, resolver", older, protocol.TaskPing, protocol.Params{Resolver: "223.5.5.5"}, protocol.CapResolver},
		{"the system resolver is what an old agent uses anyway", older, protocol.TaskPing, protocol.Params{Resolver: "system"}, ""},
		{"new agent", newer, protocol.TaskTLS, protocol.Params{ConnectTo: "1.2.3.4", Resolver: "223.5.5.5"}, ""},
	} {
		if got := missingCap(c.have, c.typ, c.p); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	// Every type that is not one of the original five names a capability.
	for _, typ := range protocol.TaskTypes {
		original := typ == protocol.TaskPing || typ == protocol.TaskTCPing || typ == protocol.TaskHTTP || typ == protocol.TaskMTR || typ == protocol.TaskDNS
		if (typ.Capability() == "") != original {
			t.Errorf("%s: capability %q", typ, typ.Capability())
		}
	}
}

// TestCapabilityGate: a node that has not announced a capability is failed
// by the server with an explanation; it is never sent the task.
func TestCapabilityGate(t *testing.T) {
	e := newTokenEnv(t)
	type node struct {
		name string
		caps []string
	}
	conns := map[string]interface{ Close() error }{}
	for _, n := range []node{{"old", []string{"tcp", "http"}}, {"new", []string{"tcp", "http", "udp", protocol.CapResolver}}} {
		var created struct {
			Token string `json:"token"`
		}
		e.call("POST", "/api/agents", `{"name":"`+n.name+`"}`, &created)
		conn, _, code, reason := e.dial(created.Token, protocol.Hello{Name: n.name, Capabilities: n.caps})
		if conn == nil {
			t.Fatalf("dial %s: %d %s", n.name, code, reason)
		}
		defer conn.Close()
		conns[n.name] = conn
		if n.name == "new" {
			go func() { // the new node is sent both tasks
				for i := 0; i < 2; i++ {
					readMsg(t, conn, protocol.MsgTask)
				}
			}()
		}
	}
	for _, body := range []string{
		`{"type":"udp","target":"192.0.2.1:53","agent_ids":["old","new"]}`,
		`{"type":"tcping","target":"192.0.2.1:443","params":{"connect_to":"192.0.2.9"},"agent_ids":["old","new"]}`,
	} {
		var snap protocol.Event
		if code := e.call("POST", "/api/tasks", body, &snap); code != 201 || len(snap.Results) != 2 {
			t.Fatalf("%s: %d %+v", body, code, snap)
		}
		for _, r := range snap.Results {
			switch r.AgentID {
			case "old":
				if r.Status != protocol.StatusError || !strings.Contains(r.Error, "unsupported") || !strings.Contains(r.Error, "缺少能力") {
					t.Errorf("%s: old node: %s %q", body, r.Status, r.Error)
				}
			case "new":
				if r.Status != protocol.StatusRunning {
					t.Errorf("%s: new node: %s %q", body, r.Status, r.Error)
				}
			}
		}
	}
}

func TestGuestParamsAllowList(t *testing.T) {
	everything := protocol.Params{
		Count: 99, IntervalMs: 10, TimeoutMs: 1500, IPVersion: "4", PublicOnly: false, Continuous: true, PacketSize: 64, Port: 8443,
		Method: "DELETE", Headers: map[string]string{"X": "y"}, Body: "b", FollowRedirects: true, InsecureTLS: true,
		ExpectStatus: 200, ExpectKeyword: "k", ExpectMaxMs: 9, SpeedTest: true, SpeedSeconds: 30,
		MaxHops: 20, Resolve: true, Protocol: "tcp", RecordType: "MX", DNSServer: "1.1.1.1",
		ConnectTo: " 1.2.3.4 ", Resolver: "223.5.5.5", UserAgent: "curl/8", Referer: "https://r", Cookie: "sid=1",
		PayloadMode: "text", Payload: "evil", QueryName: "q.example", NoWait: true, ExpectCertDays: 14, DNSServers: []string{"1.1.1.1"},
	}
	check := func(typ protocol.TaskType, want protocol.Params) {
		t.Helper()
		got := guestParams(typ, everything)
		want.PublicOnly, want.IPVersion, want.TimeoutMs = true, "4", 1500
		want.ConnectTo, want.Resolver = "1.2.3.4", "223.5.5.5"
		if g, w := string(mustJSON(got)), string(mustJSON(want)); g != w {
			t.Errorf("%s:\n got %s\nwant %s", typ, g, w)
		}
	}
	check(protocol.TaskPing, protocol.Params{Count: 60, IntervalMs: 200, Continuous: true, PacketSize: 64})
	check(protocol.TaskTCPing, protocol.Params{Count: 60, IntervalMs: 200, Continuous: true, Port: 8443})
	check(protocol.TaskHTTP, protocol.Params{Count: 3, IntervalMs: 200, Method: "GET", FollowRedirects: true, ExpectStatus: 200, ExpectKeyword: "k", ExpectMaxMs: 9, UserAgent: "curl/8"})
	check(protocol.TaskMTR, protocol.Params{Count: 10, IntervalMs: 200, MaxHops: 20, Resolve: true, Protocol: "tcp", Port: 8443})
	check(protocol.TaskDNS, protocol.Params{Count: 5, IntervalMs: 200, RecordType: "MX", DNSServer: "1.1.1.1"})
	// A guest's datagram carries the built-in payload, never their own.
	check(protocol.TaskUDP, protocol.Params{Count: 5, IntervalMs: 200, Port: 8443, PayloadMode: "text", QueryName: "q.example", NoWait: true})
	check(protocol.TaskTLS, protocol.Params{Count: 3, IntervalMs: 200, Port: 8443, ExpectCertDays: 14})
	check(protocol.TaskDNSCompare, protocol.Params{IntervalMs: 200, RecordType: "MX", DNSServers: []string{"1.1.1.1"}})
	check(protocol.TaskStatus, protocol.Params{Count: 20, IntervalMs: 200})
	// A header smuggled through the user agent is dropped with it.
	if got := guestParams(protocol.TaskHTTP, protocol.Params{UserAgent: "a\r\nX-Evil: 1"}); got.UserAgent != "" {
		t.Errorf("user agent with a line break: %q", got.UserAgent)
	}
}

func TestValidateGuestTargetNewTypes(t *testing.T) {
	old := lookupIP
	lookupIP = func(_ context.Context, _, host string) ([]net.IP, error) {
		if host == "internal.example" {
			return []net.IP{net.ParseIP("10.0.0.5")}, nil
		}
		return []net.IP{net.ParseIP("93.184.216.34")}, nil
	}
	t.Cleanup(func() { lookupIP = old })
	ctx := context.Background()
	for _, c := range []struct {
		name   string
		typ    protocol.TaskType
		target string
		p      protocol.Params
		ok     bool
	}{
		{"udp to a public host", protocol.TaskUDP, "example.com:53", protocol.Params{}, true},
		{"udp without a port", protocol.TaskUDP, "example.com", protocol.Params{}, false},
		{"udp to the LAN", protocol.TaskUDP, "192.168.1.1:53", protocol.Params{}, false},
		{"tls to a public host", protocol.TaskTLS, "example.com", protocol.Params{}, true},
		{"tls to a name that is internal", protocol.TaskTLS, "internal.example:8443", protocol.Params{}, false},
		{"dns_compare with public resolvers", protocol.TaskDNSCompare, "example.com", protocol.Params{DNSServers: []string{"system", "223.5.5.5", "1.1.1.1"}}, true},
		{"dns_compare with a LAN resolver", protocol.TaskDNSCompare, "example.com", protocol.Params{DNSServers: []string{"192.168.1.1"}}, false},
		{"dns_compare on an odd port", protocol.TaskDNSCompare, "example.com", protocol.Params{DNSServers: []string{"1.1.1.1:5353"}}, false},
		{"dns_compare with too many", protocol.TaskDNSCompare, "example.com", protocol.Params{DNSServers: []string{"1.1.1.1", "1.0.0.1", "8.8.8.8", "8.8.4.4", "9.9.9.9", "223.5.5.5", "223.6.6.6"}}, false},
		{"connect_to the LAN", protocol.TaskPing, "example.com", protocol.Params{ConnectTo: "10.0.0.1"}, false},
		{"connect_to an internal name", protocol.TaskTCPing, "example.com:443", protocol.Params{ConnectTo: "internal.example"}, false},
		{"connect_to a public address", protocol.TaskHTTP, "https://example.com/", protocol.Params{ConnectTo: "93.184.216.34"}, true},
		{"a LAN resolver", protocol.TaskPing, "example.com", protocol.Params{Resolver: "192.168.1.1"}, false},
		{"a public resolver", protocol.TaskPing, "example.com", protocol.Params{Resolver: "223.5.5.5"}, true},
		{"status", protocol.TaskStatus, "self", protocol.Params{}, false},
		{"a type nobody taught this function", protocol.TaskType("future"), "example.com", protocol.Params{}, false},
	} {
		if err := validateGuestTarget(ctx, c.typ, c.target, c.p); (err == nil) != c.ok {
			t.Errorf("%s: err=%v, want ok=%v", c.name, err, c.ok)
		}
	}
}

// TestStatusIsAdminOnly: what a node's machine is doing is not for guests.
func TestStatusIsAdminOnly(t *testing.T) {
	e := newTokenEnv(t)
	var created struct {
		Token string `json:"token"`
	}
	e.call("POST", "/api/agents", `{"name":"n1"}`, &created)
	conn, _, _, _ := e.dial(created.Token, protocol.Hello{Name: "n1", Capabilities: []string{"status"}})
	if conn == nil {
		t.Fatal("dial")
	}
	defer conn.Close()
	jar, _ := cookiejar.New(nil)
	guest := &http.Client{Jar: jar}
	if code := doJSON(t, guest, "POST", e.srv.URL+"/api/tasks", `{"type":"status","agent_ids":["n1"]}`, nil); code != http.StatusForbidden {
		t.Fatalf("guest status task: %d", code)
	}
	var snap protocol.Event
	if code := e.call("POST", "/api/tasks", `{"type":"status","agent_ids":["n1"]}`, &snap); code != 201 || snap.Task.Target != "self" {
		t.Fatalf("admin status task: %d %+v", code, snap.Task)
	}
	msg := readMsg(t, conn, protocol.MsgTask)
	var task protocol.Task
	_ = json.Unmarshal(msg.Payload, &task)
	if task.Type != protocol.TaskStatus {
		t.Fatalf("dispatched: %+v", task)
	}
	// And it cannot be shared.
	if code := e.call("POST", "/api/tasks/"+snap.Task.ID+"/shares", `{}`, nil); code != http.StatusBadRequest {
		t.Errorf("sharing a status result: %d", code)
	}
	// Nor scheduled.
	if code := e.call("POST", "/api/monitors", `{"name":"m","type":"status","target":"self","interval_sec":60}`, nil); code != http.StatusBadRequest {
		t.Errorf("status monitor: %d", code)
	}
	if code := e.call("POST", "/api/monitors", `{"name":"m","type":"dns_compare","target":"example.com","interval_sec":60}`, nil); code != http.StatusBadRequest {
		t.Errorf("dns_compare monitor: %d", code)
	}
	if code := e.call("POST", "/api/monitors", `{"name":"m","type":"tls","target":"example.com","interval_sec":60}`, nil); code != http.StatusCreated && code != http.StatusOK {
		t.Errorf("tls monitor: %d", code)
	}
}

func TestSampleFromNewTypes(t *testing.T) {
	at := time.Now()
	sample := func(typ protocol.TaskType, p protocol.Params, data any) *protocol.Sample {
		return sampleFromResult(&protocol.Monitor{ID: "m", Type: typ, Params: p}, &protocol.AgentResult{AgentID: "a", Status: protocol.StatusDone, Data: mustJSON(data)}, at)
	}
	okTLS := protocol.TLSResult{Attempts: []protocol.TLSAttempt{{OK: true, HandshakeMs: 40}}, Stats: protocol.Stats{Sent: 1, Received: 1, AvgMs: 40}, Verified: true, CertOK: true, DaysLeft: 60}
	if s := sample(protocol.TaskTLS, protocol.Params{}, okTLS); !s.OK || s.LatencyMs != 40 || s.LossPct != 0 {
		t.Errorf("good handshake: %+v", s)
	}
	bad := okTLS
	bad.Verified, bad.VerifyError = false, "x509: certificate has expired"
	if s := sample(protocol.TaskTLS, protocol.Params{}, bad); s.OK || !strings.Contains(s.Error, "expired") || s.LatencyMs != 40 {
		t.Errorf("chain does not verify: %+v", s)
	}
	soon := okTLS
	soon.CertOK, soon.DaysLeft = false, 5
	if s := sample(protocol.TaskTLS, protocol.Params{ExpectCertDays: 14}, soon); s.OK || !strings.Contains(s.Error, "5 天") {
		t.Errorf("certificate about to expire: %+v", s)
	}
	failed := protocol.TLSResult{Attempts: []protocol.TLSAttempt{{Error: "timeout"}}, Stats: protocol.Stats{Sent: 1, LossPct: 100}}
	if s := sample(protocol.TaskTLS, protocol.Params{}, failed); s.OK || s.Error != "timeout" || s.LossPct != 100 || s.LatencyMs != -1 {
		t.Errorf("handshake failed: %+v", s)
	}
	udp := protocol.PingResult{Proto: "udp", Stats: protocol.Stats{Sent: 3, Received: 2, LossPct: 33.33, AvgMs: 12}}
	if s := sample(protocol.TaskUDP, protocol.Params{}, udp); !s.OK || s.LatencyMs != 12 || s.LossPct != 33.33 {
		t.Errorf("udp: %+v", s)
	}
}
