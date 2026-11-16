package server

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"probe-platform/internal/protocol"
)

// readMsg returns the next frame of the wanted type from an agent connection.
func readMsg(t *testing.T, conn *websocket.Conn, typ string) protocol.Message {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		var m protocol.Message
		if err := conn.ReadJSON(&m); err != nil {
			t.Fatalf("waiting for %s: %v", typ, err)
		}
		if m.Type == typ {
			return m
		}
	}
}

// TestStopTaskKeepsPartialData: stopping a task keeps what an agent hands back
// in answer to the cancel, and only the agents that stay silent are failed.
func TestStopTaskKeepsPartialData(t *testing.T) {
	old := stopGrace
	stopGrace = 300 * time.Millisecond
	t.Cleanup(func() { stopGrace = old })

	e := newTokenEnv(t)
	conns := map[string]*websocket.Conn{}
	for _, name := range []string{"quick", "silent"} {
		var created struct {
			Token string `json:"token"`
		}
		if code := e.call("POST", "/api/agents", `{"name":"`+name+`"}`, &created); code != 201 {
			t.Fatalf("create %s: %d", name, code)
		}
		conn, _, code, reason := e.dial(created.Token, protocol.Hello{Name: name})
		if conn == nil {
			t.Fatalf("dial %s: %d %s", name, code, reason)
		}
		defer conn.Close()
		conns[name] = conn
	}

	var snap protocol.Event
	if code := e.call("POST", "/api/tasks", `{"type":"ping","target":"192.0.2.1","params":{"count":100,"interval_ms":1000,"continuous":true},"agent_ids":["quick","silent"]}`, &snap); code != 201 {
		t.Fatalf("create task: %d", code)
	}
	id := snap.Task.ID
	for _, c := range conns {
		readMsg(t, c, protocol.MsgTask)
	}

	if code := e.call("POST", "/api/tasks/"+id+"/stop", "", nil); code != 200 {
		t.Fatalf("stop: %d", code)
	}
	readMsg(t, conns["quick"], protocol.MsgCancel)
	partial := protocol.PingResult{Target: "192.0.2.1", IP: "192.0.2.1",
		Replies: []protocol.Reply{{Seq: 0, OK: true, RTTMs: 12}, {Seq: 1, OK: true, RTTMs: 14}},
		Stats:   protocol.Stats{Sent: 2, Received: 2, AvgMs: 13}}
	msg, _ := protocol.NewMessage(protocol.MsgResult, protocol.Result{TaskID: id, OK: false, Error: "cancelled", Data: mustJSON(partial)})
	if err := conns["quick"].WriteJSON(msg); err != nil {
		t.Fatal(err)
	}

	var got protocol.Event
	deadline := time.Now().Add(5 * time.Second)
	for {
		got = protocol.Event{}
		e.call("GET", "/api/tasks/"+id, "", &got)
		if got.Done || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !got.Done || len(got.Results) != 2 {
		t.Fatalf("task not finished after stop: %+v", got)
	}
	for _, r := range got.Results {
		switch r.AgentID {
		case "quick":
			var pr protocol.PingResult
			if r.Status != protocol.StatusDone || r.Error != protocol.ErrStopped || json.Unmarshal(r.Data, &pr) != nil || len(pr.Replies) != 2 {
				t.Fatalf("partial result lost: status=%s error=%q data=%s", r.Status, r.Error, r.Data)
			}
		case "silent":
			if r.Status != protocol.StatusError || r.Error != "cancelled" {
				t.Fatalf("silent agent: status=%s error=%q", r.Status, r.Error)
			}
		}
	}
	// It survives the trip through the store too.
	stored, err := e.st.ListResults(id)
	if err != nil || len(stored) != 2 {
		t.Fatalf("stored results: %v %d", err, len(stored))
	}
	for _, r := range stored {
		if r.AgentID == "quick" && (r.Status != protocol.StatusDone || r.Error != protocol.ErrStopped || len(r.Data) == 0) {
			t.Fatalf("stored partial result: %+v", r)
		}
	}

	// A "cancelled" result with data on a task nobody stopped is still a failure.
	e.call("POST", "/api/tasks", `{"type":"ping","target":"192.0.2.1","agent_ids":["quick"]}`, &snap)
	readMsg(t, conns["quick"], protocol.MsgTask)
	msg, _ = protocol.NewMessage(protocol.MsgResult, protocol.Result{TaskID: snap.Task.ID, OK: false, Error: "cancelled", Data: mustJSON(partial)})
	_ = conns["quick"].WriteJSON(msg)
	for i := 0; i < 100; i++ {
		got = protocol.Event{}
		e.call("GET", "/api/tasks/"+snap.Task.ID, "", &got)
		if got.Done {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !got.Done || got.Results[0].Status != protocol.StatusError {
		t.Fatalf("unsolicited cancel must stay an error: %+v", got.Results[0])
	}
}

func TestGuestContinuousCaps(t *testing.T) {
	for _, c := range []struct {
		name          string
		in            protocol.Params
		count, intrvl int
	}{
		{"defaults", protocol.Params{Continuous: true}, 60, 1000},
		{"asks for the admin default", protocol.Params{Continuous: true, Count: 100, IntervalMs: 1000}, 60, 1000},
		{"slow cadence is cut to the window", protocol.Params{Continuous: true, Count: 60, IntervalMs: 5000}, 12, 5000},
		{"fast cadence is floored", protocol.Params{Continuous: true, Count: 40, IntervalMs: 50}, 40, 200},
		{"interval longer than the window", protocol.Params{Continuous: true, Count: 5, IntervalMs: 120000}, 1, 120000},
		{"one-off keeps the old cap", protocol.Params{Count: 100}, 20, 0},
	} {
		got := guestParams(protocol.TaskPing, c.in)
		if got.Count != c.count || got.IntervalMs != c.intrvl || !got.PublicOnly {
			t.Errorf("%s: count=%d interval=%d public_only=%v, want %d / %d", c.name, got.Count, got.IntervalMs, got.PublicOnly, c.count, c.intrvl)
		}
	}
}

// TestGuestOneContinuousRun: a guest may have one continuous run at a time,
// and the flag is dropped for probe types that have no continuous mode.
func TestGuestOneContinuousRun(t *testing.T) {
	// The per-IP guest limiter is process-wide and every test comes from
	// 127.0.0.1: start from a clean window so its 429 is not mistaken for
	// ours, and leave one behind for the tests that follow.
	resetLimiter := func() {
		guestLimiter.mu.Lock()
		guestLimiter.attempts = map[string][]time.Time{}
		guestLimiter.mu.Unlock()
	}
	resetLimiter()
	t.Cleanup(resetLimiter)
	e := newTokenEnv(t)
	var created struct {
		Token string `json:"token"`
	}
	e.call("POST", "/api/agents", `{"name":"n1"}`, &created)
	conn, _, _, _ := e.dial(created.Token, protocol.Hello{Name: "n1"})
	if conn == nil {
		t.Fatal("dial")
	}
	defer conn.Close()

	jar, _ := cookiejar.New(nil)
	guest := &http.Client{Jar: jar}
	post := func(body string) (int, protocol.Event) {
		resp, err := guest.Post(e.srv.URL+"/api/tasks", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var ev protocol.Event
		_ = json.NewDecoder(resp.Body).Decode(&ev)
		return resp.StatusCode, ev
	}
	const run = `{"type":"tcping","target":"1.1.1.1:443","params":{"continuous":true},"agent_ids":["n1"]}`
	code, first := post(run)
	if code != 201 || !first.Task.Params.Continuous || first.Task.Params.Count != guestContinuousCount {
		t.Fatalf("first continuous run: %d %+v", code, first.Task)
	}
	if code, _ := post(run); code != http.StatusTooManyRequests {
		t.Fatalf("second continuous run while the first is going: %d", code)
	}
	// One-off probes are not affected, and dns has no continuous mode.
	code, dns := post(`{"type":"dns","target":"example.com","params":{"continuous":true},"agent_ids":["n1"]}`)
	if code != 201 || dns.Task.Params.Continuous {
		t.Fatalf("dns with the continuous flag: %d %+v", code, dns.Task)
	}
	// Once the first run is over a new one may start.
	e.hub.CancelTask(first.Task.ID)
	if code, _ := post(run); code != 201 {
		t.Fatalf("continuous run after the first ended: %d", code)
	}
}

func TestTaskDeadline(t *testing.T) {
	base := 3 * time.Minute
	for _, c := range []struct {
		name string
		typ  protocol.TaskType
		p    protocol.Params
		want time.Duration
	}{
		{"short ping keeps the base", protocol.TaskPing, protocol.Params{Count: 10, IntervalMs: 500}, base},
		{"100 x 1s ping fits the base", protocol.TaskPing, protocol.Params{Count: 100, IntervalMs: 1000}, base},
		{"100 x 10s ping", protocol.TaskPing, protocol.Params{Count: 100, IntervalMs: 10000, TimeoutMs: 2000}, 1022 * time.Second},
		{"tcping pays the timeout per attempt", protocol.TaskTCPing, protocol.Params{Count: 100, IntervalMs: 1000, TimeoutMs: 3000}, 420 * time.Second},
		{"count beyond the agent cap", protocol.TaskPing, protocol.Params{Count: 5000, IntervalMs: 10000, TimeoutMs: 2000}, 1022 * time.Second},
		{"capped", protocol.TaskTCPing, protocol.Params{Count: 100, IntervalMs: 10000, TimeoutMs: 10000}, maxTaskDeadline},
		{"other types keep the base", protocol.TaskHTTP, protocol.Params{Count: 20, TimeoutMs: 60000}, base},
	} {
		if got := taskDeadline(base, c.typ, c.p); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
