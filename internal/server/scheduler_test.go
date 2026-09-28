package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"probe-platform/internal/protocol"
)

func mustJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func TestSampleFromResult(t *testing.T) {
	now := time.Now()
	ping := &protocol.Monitor{ID: "m1", Type: protocol.TaskPing}
	sm := sampleFromResult(ping, &protocol.AgentResult{AgentID: "a", Status: protocol.StatusDone,
		Data: mustJSON(protocol.PingResult{Stats: protocol.Stats{Sent: 10, Received: 8, LossPct: 20, AvgMs: 12.5}})}, now)
	if !sm.OK || sm.LatencyMs != 12.5 || sm.LossPct != 20 {
		t.Fatalf("ping: %+v", sm)
	}
	sm = sampleFromResult(ping, &protocol.AgentResult{AgentID: "a", Status: protocol.StatusDone,
		Data: mustJSON(protocol.PingResult{Stats: protocol.Stats{Sent: 10, Received: 0, LossPct: 100}})}, now)
	if sm.OK || sm.LatencyMs != -1 || sm.LossPct != 100 {
		t.Fatalf("ping all lost: %+v", sm)
	}
	sm = sampleFromResult(ping, &protocol.AgentResult{AgentID: "a", Status: protocol.StatusError, Error: "agent disconnected"}, now)
	if sm.OK || sm.Error != "agent disconnected" {
		t.Fatalf("ping error: %+v", sm)
	}

	httpM := &protocol.Monitor{ID: "m2", Type: protocol.TaskHTTP}
	sm = sampleFromResult(httpM, &protocol.AgentResult{AgentID: "a", Status: protocol.StatusDone,
		Data: mustJSON(protocol.HTTPResult{Attempts: []protocol.HTTPAttempt{{OK: true, AssertOK: false, StatusCode: 500, Timing: protocol.HTTPTiming{TotalMs: 80},
			Assertions: []protocol.Assertion{{Name: "状态码", Pass: false, Detail: "500，期望 < 400"}}}}})}, now)
	if sm.OK || sm.StatusCode != 500 || sm.LatencyMs != 80 || sm.Error == "" {
		t.Fatalf("http assertion fail: %+v", sm)
	}

	// Results from agents predating assertions carry neither field: still OK.
	sm = sampleFromResult(httpM, &protocol.AgentResult{AgentID: "a", Status: protocol.StatusDone,
		Data: mustJSON(map[string]any{"attempts": []map[string]any{{"ok": true, "status_code": 200, "timing": map[string]any{"total_ms": 50}}}})}, now)
	if !sm.OK || sm.LatencyMs != 50 {
		t.Fatalf("legacy http result: %+v", sm)
	}

	mtr := &protocol.Monitor{ID: "m3", Type: protocol.TaskMTR}
	sm = sampleFromResult(mtr, &protocol.AgentResult{AgentID: "a", Status: protocol.StatusDone,
		Data: mustJSON(protocol.MTRResult{Reached: true, Hops: []protocol.MTRHop{{TTL: 1, AvgMs: 1}, {TTL: 2, AvgMs: 9, LossPct: 10}}})}, now)
	if !sm.OK || !sm.Reached || sm.LatencyMs != 9 || sm.LossPct != 10 {
		t.Fatalf("mtr: %+v", sm)
	}

	dns := &protocol.Monitor{ID: "m4", Type: protocol.TaskDNS}
	sm = sampleFromResult(dns, &protocol.AgentResult{AgentID: "a", Status: protocol.StatusDone,
		Data: mustJSON(protocol.DNSResult{Attempts: []protocol.DNSAttempt{{OK: false, RCode: "NameError"}}})}, now)
	if sm.OK || sm.Error != "rcode NameError" {
		t.Fatalf("dns: %+v", sm)
	}
}

func TestAlertStateMachine(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	var mu sync.Mutex
	var received []map[string]any
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		mu.Lock()
		received = append(received, m)
		mu.Unlock()
	}))
	defer hook.Close()
	ch := &protocol.NotifyChannel{ID: "n1", Name: "hook", Type: "webhook", Config: map[string]string{"url": hook.URL}, Enabled: true}
	if err := st.UpsertChannel(ch); err != nil {
		t.Fatal(err)
	}
	m := &protocol.Monitor{ID: "m1", Name: "阿里DNS", Type: protocol.TaskPing, Target: "223.5.5.5", IntervalSec: 60, Enabled: true,
		Alert: protocol.AlertRule{Enabled: true, LossPct: 50, Consecutive: 2}, NotifyIDs: []string{"n1"}}
	if err := st.UpsertMonitor(m); err != nil {
		t.Fatal(err)
	}
	cfg := Config{AgentToken: "tok", TaskTimeout: time.Minute}
	hub := NewHub(cfg, st, nil, nil, discardLogger())
	s := NewScheduler(st, hub, NewNotifier(discardLogger()), func() string { return "https://probe.example.com" }, discardLogger())

	agents := map[string]*protocol.AgentResult{
		"a1": {AgentID: "a1", AgentName: "home", Location: "广东 深圳", ISP: "电信"},
		"a2": {AgentID: "a2", AgentName: "nas"},
		"a3": {AgentID: "a3", AgentName: "cloud"},
	}
	ping := func(loss float64) json.RawMessage {
		return mustJSON(protocol.PingResult{Stats: protocol.Stats{Sent: 10, Received: 10 - int(loss/10), LossPct: loss, AvgMs: 10}})
	}
	// run finishes one monitor task; loss maps agent id -> loss %.
	run := func(loss map[string]float64) {
		var results []*protocol.AgentResult
		for _, id := range []string{"a1", "a2", "a3"} {
			r := *agents[id]
			r.TaskID, r.Status, r.Data = "t", protocol.StatusDone, ping(loss[id])
			results = append(results, &r)
		}
		s.onTaskDone(protocol.Task{ID: "t", MonitorID: "m1"}, results)
	}
	waitCalls := func(n int) []map[string]any {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			mu.Lock()
			got := append([]map[string]any(nil), received...)
			mu.Unlock()
			if len(got) >= n || time.Now().After(deadline) {
				time.Sleep(100 * time.Millisecond) // catch unexpected extras
				mu.Lock()
				got = append([]map[string]any(nil), received...)
				mu.Unlock()
				if len(got) != n {
					t.Fatalf("expected %d webhook calls, got %d: %v", n, len(got), got)
				}
				return got
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	run(map[string]float64{"a1": 60, "a2": 60}) // 1st failure: no alert yet
	state, _ := st.GetState("m1", "a1")
	if state.Failing != 1 || state.Alerting {
		t.Fatalf("after 1 failure: %+v", state)
	}
	waitCalls(0)

	run(map[string]float64{"a1": 60, "a2": 60}) // 2nd: both nodes alert, one message
	state, _ = st.GetState("m1", "a1")
	if state.Failing != 2 || !state.Alerting || state.Since == nil {
		t.Fatalf("after 2 failures: %+v", state)
	}
	calls := waitCalls(1)
	if title, _ := calls[0]["title"].(string); title != "【拨测告警】阿里DNS 异常（2/3 节点失败）" {
		t.Fatalf("down title: %q", title)
	}
	text, _ := calls[0]["text"].(string)
	if !containsAll(text, "失败 2 个节点", "· home（广东 深圳 电信）：丢包 60%，阈值 50%（新告警，连续 2 次）", "· nas：丢包 60%",
		"正常 1 个节点", "· cloud：10.0 ms", "https://probe.example.com/monitors?id=m1") {
		t.Fatalf("down text: %q", text)
	}

	run(map[string]float64{"a1": 60, "a2": 60}) // still alerting: nothing new
	waitCalls(1)

	run(map[string]float64{"a2": 60}) // home recovers, nas still down
	calls = waitCalls(2)
	var partial string
	for _, c := range calls {
		if title, _ := c["title"].(string); title == "【拨测恢复】阿里DNS 部分恢复（1/3 节点失败）" {
			partial, _ = c["text"].(string)
		}
	}
	if !containsAll(partial, "· nas：丢包 60%，阈值 50%（告警中，已持续", "· home（广东 深圳 电信）：10.0 ms（已恢复，异常持续") {
		t.Fatalf("partial recovery: %q (all: %v)", partial, calls)
	}

	run(nil) // everyone healthy again
	calls = waitCalls(3)
	found := false
	for _, c := range calls {
		if title, _ := c["title"].(string); title == "【拨测恢复】阿里DNS 恢复正常" {
			found = true
		}
	}
	if !found {
		t.Fatalf("full recovery missing: %v", calls)
	}

	events, _ := st.ListAlertEvents("m1", 10)
	if len(events) != 4 || events[0].Kind != "up" || events[3].Kind != "down" || events[3].Monitor != "阿里DNS" {
		t.Fatalf("events: %+v", events)
	}
	if n, _ := st.AlertingCount(); n != 0 {
		t.Fatalf("alerting count: %d", n)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !contains(s, sub) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestSeriesBucketing(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	base := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		_ = st.InsertSample(&protocol.Sample{MonitorID: "m", AgentID: "a", At: base.Add(time.Duration(i) * time.Minute), OK: i != 3, LatencyMs: float64(10 + i), LossPct: float64(i * 10)})
	}
	_ = st.InsertSample(&protocol.Sample{MonitorID: "m", AgentID: "b", At: base, OK: true, LatencyMs: 5})
	raw, err := st.Series("m", base, base.Add(time.Hour), 0)
	if err != nil || len(raw) != 2 || len(raw[0].Points) != 10 || raw[1].AgentID != "b" {
		t.Fatalf("raw: %v %+v", err, raw)
	}
	bucketed, err := st.Series("m", base, base.Add(time.Hour), 5*time.Minute)
	if err != nil || len(bucketed) != 2 || len(bucketed[0].Points) != 2 {
		t.Fatalf("bucketed: %v %+v", err, bucketed)
	}
	p := bucketed[0].Points[0]
	if p.Count != 5 || p.LatencyMs != 12 || p.OKRatio != 0.8 {
		t.Fatalf("bucket 0: %+v", p)
	}
	latest, _ := st.LatestSamples("m")
	if latest["a"].LatencyMs != 19 || latest["b"].LatencyMs != 5 {
		t.Fatalf("latest: %+v", latest)
	}
	if n, _ := st.PruneSamples(base.Add(5 * time.Minute)); n != 6 {
		t.Fatalf("prune: %d", n)
	}
}
