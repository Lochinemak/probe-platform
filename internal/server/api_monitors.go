package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"probe-platform/internal/protocol"
)

// monitorView is what the dashboard list shows per monitor.
type monitorView struct {
	*protocol.Monitor
	Agents   []*protocol.MonitorAgentState `json:"agents"`
	Alerting int                           `json:"alerting"`
}

func (a *API) monitorView(m *protocol.Monitor, agents map[string]*protocol.AgentStatus) *monitorView {
	v := &monitorView{Monitor: m}
	latest, _ := a.store.LatestSamples(m.ID)
	states, _ := a.store.ListStates(m.ID)
	byAgent := map[string]*protocol.MonitorAgentState{}
	for _, st := range states {
		byAgent[st.AgentID] = st
	}
	ids := map[string]bool{}
	for id := range latest {
		ids[id] = true
	}
	for id := range byAgent {
		ids[id] = true
	}
	for _, id := range m.AgentIDs {
		ids[id] = true
	}
	for id := range ids {
		st := byAgent[id]
		if st == nil {
			st = &protocol.MonitorAgentState{MonitorID: m.ID, AgentID: id}
		}
		st.Last = latest[id]
		if ag := agents[id]; ag != nil {
			st.AgentName = ag.Name
		} else {
			st.AgentName = id
		}
		if st.Alerting {
			v.Alerting++
		}
		v.Agents = append(v.Agents, st)
	}
	sort.Slice(v.Agents, func(i, j int) bool { return v.Agents[i].AgentName < v.Agents[j].AgentName })
	if v.Agents == nil {
		v.Agents = []*protocol.MonitorAgentState{}
	}
	return v
}

func (a *API) agentIndex() map[string]*protocol.AgentStatus {
	out := map[string]*protocol.AgentStatus{}
	for _, ag := range a.hub.Agents() {
		out[ag.ID] = ag
	}
	return out
}

func (a *API) listMonitors(w http.ResponseWriter, _ *http.Request) {
	monitors, err := a.store.ListMonitors()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	agents := a.agentIndex()
	views := make([]*monitorView, 0, len(monitors))
	for _, m := range monitors {
		views = append(views, a.monitorView(m, agents))
	}
	writeJSON(w, http.StatusOK, map[string]any{"monitors": views})
}

var intervalChoices = map[int]bool{30: true, 60: true, 120: true, 300: true, 600: true, 900: true, 1800: true, 3600: true, 7200: true, 21600: true, 43200: true, 86400: true}

func validateMonitor(m *protocol.Monitor) error {
	m.Name = strings.TrimSpace(m.Name)
	m.Target = strings.TrimSpace(m.Target)
	if m.Name == "" {
		m.Name = m.Target
	}
	if len(m.Name) > 100 {
		return errors.New("name too long")
	}
	if !m.Type.Valid() {
		return errors.New("invalid type")
	}
	if m.Target == "" || len(m.Target) > 2048 {
		return errors.New("target is required")
	}
	if !intervalChoices[m.IntervalSec] {
		if m.IntervalSec < 30 || m.IntervalSec > 86400 {
			return errors.New("interval must be between 30s and 24h")
		}
	}
	if m.Alert.Consecutive <= 0 {
		m.Alert.Consecutive = 2
	}
	if m.Alert.Consecutive > 20 {
		m.Alert.Consecutive = 20
	}
	if m.Params.Count > 100 {
		m.Params.Count = 100
	}
	if m.AgentIDs == nil {
		m.AgentIDs = []string{}
	}
	if m.NotifyIDs == nil {
		m.NotifyIDs = []string{}
	}
	return nil
}

func (a *API) createMonitor(w http.ResponseWriter, r *http.Request) {
	var m protocol.Monitor
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&m); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if err := validateMonitor(&m); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	m.ID = "m" + newTaskID()
	now := time.Now()
	m.CreatedAt = now
	m.NextRunAt = &now // first run on the next scheduler tick
	if err := a.store.UpsertMonitor(&m); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.log.Info("monitor created", "id", m.ID, "name", m.Name, "type", m.Type, "target", m.Target, "interval", m.IntervalSec)
	writeJSON(w, http.StatusCreated, a.monitorView(&m, a.agentIndex()))
}

func (a *API) getMonitor(w http.ResponseWriter, r *http.Request) {
	m, err := a.store.GetMonitor(r.PathValue("id"))
	if errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusNotFound, "monitor not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a.monitorView(m, a.agentIndex()))
}

func (a *API) updateMonitor(w http.ResponseWriter, r *http.Request) {
	cur, err := a.store.GetMonitor(r.PathValue("id"))
	if errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusNotFound, "monitor not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var m protocol.Monitor
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&m); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if err := validateMonitor(&m); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	m.ID, m.CreatedAt, m.LastRunAt = cur.ID, cur.CreatedAt, cur.LastRunAt
	m.NextRunAt = cur.NextRunAt
	if m.IntervalSec != cur.IntervalSec || (m.Enabled && !cur.Enabled) {
		now := time.Now()
		m.NextRunAt = &now
	}
	if err := a.store.UpsertMonitor(&m); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a.monitorView(&m, a.agentIndex()))
}

func (a *API) deleteMonitor(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DeleteMonitor(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) runMonitor(w http.ResponseWriter, r *http.Request) {
	m, err := a.store.GetMonitor(r.PathValue("id"))
	if errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusNotFound, "monitor not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if a.sched == nil {
		writeErr(w, http.StatusServiceUnavailable, "scheduler not running")
		return
	}
	taskID, err := a.sched.RunNow(m, true)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"task_id": taskID})
}

// monitorSeries returns chart data. ?range=1h|6h|24h|7d|30d (default 6h) or
// ?from=&to= (unix ms); ?bucket= seconds overrides the automatic bucket.
func (a *API) monitorSeries(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m, err := a.store.GetMonitor(id)
	if errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusNotFound, "monitor not found")
		return
	}
	q := r.URL.Query()
	to := time.Now()
	from := to.Add(-6 * time.Hour)
	if v := q.Get("range"); v != "" {
		if d, err := parseRange(v); err == nil {
			from = to.Add(-d)
		}
	}
	if v, err := strconv.ParseInt(q.Get("from"), 10, 64); err == nil && v > 0 {
		from = time.UnixMilli(v)
	}
	if v, err := strconv.ParseInt(q.Get("to"), 10, 64); err == nil && v > 0 {
		to = time.UnixMilli(v)
	}
	span := to.Sub(from)
	var bucket time.Duration
	switch {
	case span <= 6*time.Hour:
		bucket = 0
	case span <= 48*time.Hour:
		bucket = 5 * time.Minute
	case span <= 8*24*time.Hour:
		bucket = 30 * time.Minute
	default:
		bucket = 2 * time.Hour
	}
	if v, err := strconv.Atoi(q.Get("bucket")); err == nil && v >= 0 {
		bucket = time.Duration(v) * time.Second
	}
	series, err := a.store.Series(id, from, to, bucket)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	agents := a.agentIndex()
	for _, s := range series {
		if ag := agents[s.AgentID]; ag != nil {
			s.AgentName = ag.Name
		} else {
			s.AgentName = s.AgentID
		}
	}
	if series == nil {
		series = []*protocol.Series{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"monitor": m, "from": from.UnixMilli(), "to": to.UnixMilli(), "bucket_sec": int(bucket.Seconds()), "series": series,
	})
}

func parseRange(v string) (time.Duration, error) {
	v = strings.TrimSpace(strings.ToLower(v))
	if strings.HasSuffix(v, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(v, "d"))
		if err != nil {
			return 0, err
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(v)
}

func (a *API) monitorAlerts(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := a.store.ListAlertEvents(r.PathValue("id"), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (a *API) listAlerts(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := a.store.ListAlertEvents("", limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := a.store.AlertingCount()
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "alerting": n})
}

// --- notification channels --------------------------------------------------

func (a *API) listChannels(w http.ResponseWriter, _ *http.Request) {
	chs, err := a.store.ListChannels()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": chs, "types": ChannelTypes})
}

func decodeChannel(r *http.Request) (*protocol.NotifyChannel, error) {
	var c protocol.NotifyChannel
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&c); err != nil {
		return nil, errors.New("invalid json: " + err.Error())
	}
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		return nil, errors.New("name is required")
	}
	if _, ok := ChannelTypes[c.Type]; !ok {
		return nil, errors.New("unknown channel type")
	}
	if c.Config == nil {
		c.Config = map[string]string{}
	}
	for k, v := range c.Config {
		c.Config[k] = strings.TrimSpace(v)
	}
	return &c, nil
}

func (a *API) createChannel(w http.ResponseWriter, r *http.Request) {
	c, err := decodeChannel(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	c.ID = "n" + newTaskID()
	c.CreatedAt = time.Now()
	if err := a.store.UpsertChannel(c); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (a *API) updateChannel(w http.ResponseWriter, r *http.Request) {
	cur, err := a.store.GetChannel(r.PathValue("id"))
	if errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusNotFound, "channel not found")
		return
	}
	c, err := decodeChannel(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	c.ID, c.CreatedAt = cur.ID, cur.CreatedAt
	if err := a.store.UpsertChannel(c); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (a *API) deleteChannel(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DeleteChannel(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) testChannel(w http.ResponseWriter, r *http.Request) {
	c, err := a.store.GetChannel(r.PathValue("id"))
	if errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusNotFound, "channel not found")
		return
	}
	if a.notifier == nil {
		writeErr(w, http.StatusServiceUnavailable, "notifier unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := a.notifier.Send(ctx, c, "【拨测平台】测试通知", "如果你看到这条消息，说明通知渠道「"+c.Name+"」配置正确。\n时间："+time.Now().Format("2006-01-02 15:04:05")); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
