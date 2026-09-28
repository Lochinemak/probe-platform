package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"probe-platform/internal/protocol"
)

// Scheduler runs monitors on their intervals, turns finished tasks into
// samples, drives the per-agent alert state machine and sends notifications.
type Scheduler struct {
	store    *Store
	hub      *Hub
	notifier *Notifier
	log      *slog.Logger
	baseURL  func() string // for links in notifications; may return ""

	mu      sync.Mutex
	running map[string]bool // monitor ids with a task in flight
}

// NewScheduler wires the scheduler into the hub's task-done hook.
func NewScheduler(store *Store, hub *Hub, notifier *Notifier, baseURL func() string, log *slog.Logger) *Scheduler {
	if baseURL == nil {
		baseURL = func() string { return "" }
	}
	s := &Scheduler{store: store, hub: hub, notifier: notifier, log: log, baseURL: baseURL, running: map[string]bool{}}
	hub.SetTaskDoneHook(s.onTaskDone)
	return s
}

// Run ticks until ctx ends.
func (s *Scheduler) Run(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick(time.Now())
		}
	}
}

func (s *Scheduler) tick(now time.Time) {
	due, err := s.store.DueMonitors(now)
	if err != nil {
		s.log.Error("scheduler: list due monitors", "err", err)
		return
	}
	for _, m := range due {
		s.RunNow(m, false)
	}
}

// RunNow launches one run of m. manual runs do not move the schedule.
func (s *Scheduler) RunNow(m *protocol.Monitor, manual bool) (string, error) {
	now := time.Now()
	interval := time.Duration(m.IntervalSec) * time.Second
	if interval < 10*time.Second {
		interval = 10 * time.Second
	}
	if !manual {
		if err := s.store.MarkMonitorRun(m.ID, now, now.Add(interval)); err != nil {
			s.log.Error("scheduler: mark run", "monitor", m.ID, "err", err)
		}
	}
	s.mu.Lock()
	if s.running[m.ID] && !manual {
		s.mu.Unlock()
		s.log.Warn("scheduler: previous run still in flight, skipping", "monitor", m.ID)
		return "", nil
	}
	s.running[m.ID] = true
	s.mu.Unlock()

	snap, err := s.hub.CreateTaskWithMonitor(m.Type, m.Target, m.Params, m.AgentIDs, m.ID)
	if err != nil {
		s.mu.Lock()
		delete(s.running, m.ID)
		s.mu.Unlock()
		s.log.Warn("scheduler: run failed to start", "monitor", m.ID, "err", err)
		return "", err
	}
	return snap.Task.ID, nil
}

// onTaskDone is called by the hub when every agent of a task has finished.
func (s *Scheduler) onTaskDone(task protocol.Task, results []*protocol.AgentResult) {
	if task.MonitorID == "" {
		return
	}
	s.mu.Lock()
	delete(s.running, task.MonitorID)
	s.mu.Unlock()

	m, err := s.store.GetMonitor(task.MonitorID)
	if err != nil {
		return // deleted meanwhile
	}
	now := time.Now()
	var outcomes []*agentOutcome
	for _, r := range results {
		sm := sampleFromResult(m, r, now)
		if err := s.store.InsertSample(sm); err != nil {
			s.log.Error("scheduler: store sample", "err", err)
			continue
		}
		if o := s.evaluate(m, r, sm, now); o != nil {
			outcomes = append(outcomes, o)
		}
	}
	s.notifyRun(m, outcomes, now)
}

// sampleFromResult reduces one agent's result to latency / loss / ok.
func sampleFromResult(m *protocol.Monitor, r *protocol.AgentResult, at time.Time) *protocol.Sample {
	sm := &protocol.Sample{MonitorID: m.ID, AgentID: r.AgentID, At: at, TaskID: r.TaskID, LatencyMs: -1}
	if r.Status != protocol.StatusDone {
		sm.Error = r.Error
		if sm.Error == "" {
			sm.Error = string(r.Status)
		}
		sm.LossPct = 100
		return sm
	}
	switch m.Type {
	case protocol.TaskPing, protocol.TaskTCPing:
		var pr protocol.PingResult
		if json.Unmarshal(r.Data, &pr) != nil {
			sm.Error = "unreadable result"
			sm.LossPct = 100
			return sm
		}
		sm.LossPct = pr.Stats.LossPct
		if pr.Stats.Received > 0 {
			sm.LatencyMs = pr.Stats.AvgMs
			sm.OK = true
		} else {
			sm.Error = "100% loss"
		}
	case protocol.TaskHTTP:
		var hr protocol.HTTPResult
		if json.Unmarshal(r.Data, &hr) != nil || len(hr.Attempts) == 0 {
			sm.Error = "unreadable result"
			sm.LossPct = 100
			return sm
		}
		// Agents older than the assertion feature send no assertions and no
		// assert_ok; treat those as passing rather than failing every run.
		assertOK := func(a protocol.HTTPAttempt) bool { return a.AssertOK || len(a.Assertions) == 0 }
		okN := 0
		for _, a := range hr.Attempts {
			if a.OK && assertOK(a) {
				okN++
			}
		}
		last := hr.Attempts[len(hr.Attempts)-1]
		sm.LossPct = float64(len(hr.Attempts)-okN) / float64(len(hr.Attempts)) * 100
		sm.StatusCode = last.StatusCode
		sm.ThroughputMbps = last.ThroughputMbps
		if last.OK {
			sm.LatencyMs = last.Timing.TotalMs
		}
		sm.OK = last.OK && assertOK(last)
		if !sm.OK {
			sm.Error = last.Error
			if sm.Error == "" {
				for _, as := range last.Assertions {
					if !as.Pass {
						sm.Error = as.Name + "：" + as.Detail
						break
					}
				}
			}
		}
	case protocol.TaskDNS:
		var dr protocol.DNSResult
		if json.Unmarshal(r.Data, &dr) != nil || len(dr.Attempts) == 0 {
			sm.Error = "unreadable result"
			sm.LossPct = 100
			return sm
		}
		okN := 0
		for _, a := range dr.Attempts {
			if a.OK {
				okN++
			}
		}
		last := dr.Attempts[len(dr.Attempts)-1]
		sm.LossPct = float64(len(dr.Attempts)-okN) / float64(len(dr.Attempts)) * 100
		sm.OK = last.OK
		if last.OK {
			sm.LatencyMs = last.RTTMs
		} else {
			sm.Error = last.Error
			if sm.Error == "" {
				sm.Error = "rcode " + last.RCode
			}
		}
	case protocol.TaskMTR:
		var mr protocol.MTRResult
		if json.Unmarshal(r.Data, &mr) != nil {
			sm.Error = "unreadable result"
			sm.LossPct = 100
			return sm
		}
		sm.Reached = mr.Reached
		sm.OK = mr.Reached
		if mr.Reached && len(mr.Hops) > 0 {
			dest := mr.Hops[len(mr.Hops)-1]
			sm.LatencyMs = dest.AvgMs
			sm.LossPct = dest.LossPct
		} else {
			sm.LossPct = 100
			sm.Error = "destination not reached"
		}
	}
	return sm
}

// agentOutcome is one agent's verdict in a monitor run, collected so the
// whole run goes out as a single notification.
type agentOutcome struct {
	label    string // name plus location / ISP
	reason   string // why the alert rule failed; "" = passed
	sample   *protocol.Sample
	kind     string // "down" / "up" when this run changed the agent's alert state
	failing  int    // consecutive failures including this run
	need     int    // failures needed to alert
	alerting bool   // alert state after this run
	since    *time.Time
}

// evaluate applies the alert rule to one agent's sample, advances its alert
// state and records a down / up event on transitions. It returns nil when
// alerting is off or the state cannot be loaded.
func (s *Scheduler) evaluate(m *protocol.Monitor, r *protocol.AgentResult, sm *protocol.Sample, now time.Time) *agentOutcome {
	if !m.Alert.Enabled {
		return nil
	}
	reason := ""
	switch {
	case !sm.OK:
		reason = sm.Error
		if reason == "" {
			reason = "探测失败"
		}
	case m.Alert.LossPct > 0 && sm.LossPct >= m.Alert.LossPct:
		reason = fmt.Sprintf("丢包 %.0f%%，阈值 %.0f%%", sm.LossPct, m.Alert.LossPct)
	case m.Alert.LatencyMs > 0 && sm.LatencyMs >= m.Alert.LatencyMs:
		reason = fmt.Sprintf("延迟 %.0f ms，阈值 %.0f ms", sm.LatencyMs, m.Alert.LatencyMs)
	}
	st, err := s.store.GetState(m.ID, r.AgentID)
	if err != nil {
		s.log.Error("scheduler: load state", "err", err)
		return nil
	}
	need := m.Alert.Consecutive
	if need <= 0 {
		need = 2
	}
	label := r.AgentName
	if label == "" {
		label = r.AgentID
	}
	if place := strings.TrimSpace(strings.Join([]string{r.Location, r.ISP}, " ")); place != "" {
		label += "（" + place + "）"
	}
	o := &agentOutcome{label: label, reason: reason, sample: sm, need: need}
	if reason != "" {
		st.Failing++
		st.LastError = reason
		if !st.Alerting && st.Failing >= need {
			st.Alerting = true
			st.Since = &now
			o.kind = "down"
			s.recordEvent(m, r, "down", fmt.Sprintf("连续 %d 次失败：%s", st.Failing, reason), now)
		}
		o.since = st.Since
	} else {
		if st.Alerting {
			o.kind = "up"
			o.since = st.Since
			s.recordEvent(m, r, "up", "已恢复"+sinceText(st.Since, now, "，持续 ")+describeSample(sm), now)
		}
		st.Failing = 0
		st.Alerting = false
		st.Since = nil
		st.LastError = ""
	}
	o.failing = st.Failing
	o.alerting = st.Alerting
	if err := s.store.SaveState(st); err != nil {
		s.log.Error("scheduler: save state", "err", err)
	}
	return o
}

func describeSample(sm *protocol.Sample) string {
	if sm.LatencyMs >= 0 {
		return fmt.Sprintf("（延迟 %.1f ms，丢包 %.0f%%）", sm.LatencyMs, sm.LossPct)
	}
	return ""
}

// sinceText renders how long an alert has lasted, prefixed; "" when unknown.
func sinceText(since *time.Time, now time.Time, prefix string) string {
	if since == nil {
		return ""
	}
	return prefix + now.Sub(*since).Round(time.Second).String()
}

func (s *Scheduler) recordEvent(m *protocol.Monitor, r *protocol.AgentResult, kind, msg string, at time.Time) {
	ev := &protocol.AlertEvent{MonitorID: m.ID, AgentID: r.AgentID, Kind: kind, Message: msg, At: at}
	if err := s.store.InsertAlertEvent(ev); err != nil {
		s.log.Error("scheduler: store alert", "err", err)
	}
	s.log.Info("alert", "kind", kind, "monitor", m.Name, "agent", r.AgentID, "detail", msg)
}

// notifyRun sends one message for a whole monitor run, listing every agent
// that failed (with its error) and every agent that passed. Nothing is sent
// unless at least one agent's alert state changed in this run, so a lasting
// outage is reported once when it starts and once when it ends.
func (s *Scheduler) notifyRun(m *protocol.Monitor, outcomes []*agentOutcome, at time.Time) {
	changed := false
	anyDown := false
	var failed, passed []*agentOutcome
	for _, o := range outcomes {
		if o.kind != "" {
			changed = true
		}
		if o.kind == "down" {
			anyDown = true
		}
		if o.reason != "" {
			failed = append(failed, o)
		} else {
			passed = append(passed, o)
		}
	}
	if !changed {
		return
	}
	byLabel := func(list []*agentOutcome) {
		sort.SliceStable(list, func(i, j int) bool { return list[i].label < list[j].label })
	}
	byLabel(failed)
	byLabel(passed)

	total := len(outcomes)
	var title string
	switch {
	case anyDown:
		title = fmt.Sprintf("【拨测告警】%s 异常（%d/%d 节点失败）", m.Name, len(failed), total)
	case len(failed) > 0:
		title = fmt.Sprintf("【拨测恢复】%s 部分恢复（%d/%d 节点失败）", m.Name, len(failed), total)
	default:
		title = "【拨测恢复】" + m.Name + " 恢复正常"
	}

	lines := []string{"目标：" + strings.ToUpper(string(m.Type)) + " " + m.Target}
	if len(failed) > 0 {
		lines = append(lines, fmt.Sprintf("失败 %d 个节点：", len(failed)))
		for _, o := range failed {
			var note string
			switch {
			case o.kind == "down":
				note = fmt.Sprintf("新告警，连续 %d 次", o.failing)
			case o.alerting:
				note = "告警中" + sinceText(o.since, at, "，已持续 ")
			default:
				note = fmt.Sprintf("连续 %d 次，未达 %d 次告警条件", o.failing, o.need)
			}
			lines = append(lines, "· "+o.label+"："+o.reason+"（"+note+"）")
		}
	}
	if len(passed) > 0 {
		lines = append(lines, fmt.Sprintf("正常 %d 个节点：", len(passed)))
		for _, o := range passed {
			line := "· " + o.label
			if o.sample.LatencyMs >= 0 {
				line += fmt.Sprintf("：%.1f ms", o.sample.LatencyMs)
				if o.sample.LossPct > 0 {
					line += fmt.Sprintf("，丢包 %.0f%%", o.sample.LossPct)
				}
			}
			if o.kind == "up" {
				line += "（已恢复" + sinceText(o.since, at, "，异常持续 ") + "）"
			}
			lines = append(lines, line)
		}
	}
	lines = append(lines, "时间："+at.Format("2006-01-02 15:04:05"))
	if base := strings.TrimSuffix(s.baseURL(), "/"); base != "" {
		lines = append(lines, "详情："+base+"/#/monitor/"+m.ID)
	}
	text := strings.Join(lines, "\n")
	for _, id := range m.NotifyIDs {
		ch, err := s.store.GetChannel(id)
		if err != nil || !ch.Enabled {
			continue
		}
		go func(ch *protocol.NotifyChannel) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if err := s.notifier.Send(ctx, ch, title, text); err != nil {
				s.log.Warn("notification failed", "channel", ch.Name, "type", ch.Type, "err", err)
			}
		}(ch)
	}
}
