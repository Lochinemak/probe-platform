package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"probe-platform/internal/buildinfo"
	"probe-platform/internal/protocol"
)

const (
	agentReadTimeout  = 90 * time.Second
	agentPingInterval = 30 * time.Second
	finishedTaskTTL   = 10 * time.Minute
	maxProgressEvents = 1000
)

// Hub tracks connected agents and in-flight tasks.
type Hub struct {
	cfg   Config
	store *Store
	geo   *GeoIP
	files *AgentFiles // nil when self-update is disabled
	log   *slog.Logger

	// taskDone is invoked (in its own goroutine) once every agent of a task
	// has reached a terminal state. Set by the scheduler.
	taskDone func(protocol.Task, []*protocol.AgentResult)

	mu     sync.RWMutex
	agents map[string]*agentConn
	tasks  map[string]*taskRun

	upgrader websocket.Upgrader
}

// NewHub creates a hub and starts its background janitor.
func NewHub(cfg Config, store *Store, geo *GeoIP, files *AgentFiles, log *slog.Logger) *Hub {
	h := &Hub{
		cfg:    cfg,
		store:  store,
		geo:    geo,
		files:  files,
		log:    log,
		agents: map[string]*agentConn{},
		tasks:  map[string]*taskRun{},
		upgrader: websocket.Upgrader{
			ReadBufferSize:  16 << 10,
			WriteBufferSize: 16 << 10,
			CheckOrigin:     func(*http.Request) bool { return true }, // agents are not browsers
		},
	}
	go h.janitor()
	return h
}

// SetTaskDoneHook registers the completion callback.
func (h *Hub) SetTaskDoneHook(fn func(protocol.Task, []*protocol.AgentResult)) { h.taskDone = fn }

// notifyDone snapshots a finished task and hands it to the hook.
func (h *Hub) notifyDone(tr *taskRun) {
	if h.taskDone == nil {
		return
	}
	tr.mu.Lock()
	snap := tr.snapshotLocked()
	tr.mu.Unlock()
	go h.taskDone(*snap.Task, snap.Results)
}

// --- agent connections ------------------------------------------------------

type agentConn struct {
	id          string
	conn        *websocket.Conn
	send        chan protocol.Message
	done        chan struct{}
	closeOnce   sync.Once
	connectedAt time.Time
	running     atomic.Int32

	mu     sync.RWMutex
	status protocol.AgentStatus
}

func (ac *agentConn) close() {
	ac.closeOnce.Do(func() {
		close(ac.done)
		ac.conn.Close()
	})
}

func (ac *agentConn) snapshot() protocol.AgentStatus {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	s := ac.status
	s.Online = true
	t := ac.connectedAt
	s.ConnectedAt = &t
	s.Running = int(ac.running.Load())
	s.LastSeen = time.Now()
	return s
}

// trySend enqueues a message without blocking.
func (ac *agentConn) trySend(m protocol.Message) bool {
	select {
	case ac.send <- m:
		return true
	case <-ac.done:
		return false
	default:
		return false
	}
}

// clientIP identifies the caller for rate limiting and audit logs.
//
// X-Forwarded-For is a list the client starts and every proxy appends to, so
// its leftmost entry is whatever the client claimed. Only the entries our own
// proxies added can be trusted: with hops=1 (one reverse proxy, the usual
// setup) that is the last one. Anything shorter than hops means the header did
// not come through the expected chain, so we fall back to the peer address.
func clientIP(r *http.Request, trustProxy bool, hops int) string {
	direct := remoteHost(r)
	if !trustProxy {
		return direct
	}
	if hops < 1 {
		hops = 1
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if i := len(parts) - hops; i >= 0 {
			if ip := strings.TrimSpace(parts[i]); net.ParseIP(ip) != nil {
				return ip
			}
		}
	}
	// Set by the proxy itself (nginx: $remote_addr), so it cannot be forged
	// through the chain the way the X-Forwarded-For list can.
	if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(xr) != nil {
		return xr
	}
	return direct
}

func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func agentIDFromName(name string) string {
	name = strings.TrimSpace(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == ' ' || r == '\t' || r == '/' || r == '\\' || r == '?' || r == '#':
			b.WriteRune('-')
		case r < 0x20:
		default:
			b.WriteRune(r)
		}
	}
	id := strings.Trim(b.String(), "-")
	if len(id) > 64 {
		id = id[:64]
	}
	return id
}

// checkToken accepts the shared agent secret as a bearer token. The token is
// deliberately not read from the query string: that would put it in every
// reverse-proxy access log.
func (h *Hub) checkToken(r *http.Request) bool {
	if h.cfg.AgentToken == "" {
		return false
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(auth, "Bearer ")), []byte(h.cfg.AgentToken)) == 1
}

// HandleAgentWS is the WebSocket endpoint agents connect to.
func (h *Hub) HandleAgentWS(w http.ResponseWriter, r *http.Request) {
	if !h.checkToken(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	remoteIP := h.cfg.ClientIP(r)
	conn.SetReadLimit(4 << 20)
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	var first protocol.Message
	if err := conn.ReadJSON(&first); err != nil || first.Type != protocol.MsgHello {
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "expected hello"), time.Now().Add(time.Second))
		conn.Close()
		return
	}
	var hello protocol.Hello
	if err := json.Unmarshal(first.Payload, &hello); err != nil {
		conn.Close()
		return
	}
	id := agentIDFromName(hello.Name)
	if id == "" {
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "empty agent name"), time.Now().Add(time.Second))
		conn.Close()
		return
	}

	now := time.Now()
	ac := &agentConn{
		id:          id,
		conn:        conn,
		send:        make(chan protocol.Message, 256),
		done:        make(chan struct{}),
		connectedAt: now,
		status: protocol.AgentStatus{
			ID: id, Name: hello.Name, Location: hello.Location, ISP: hello.ISP, Tags: hello.Tags,
			OS: hello.OS, Arch: hello.Arch, Version: hello.Version, PublicIP: remoteIP,
			Capabilities: hello.Capabilities, LastSeen: now,
		},
	}
	if prev, err := h.store.GetAgent(id); err == nil {
		ac.status.FirstSeen = prev.FirstSeen
		ac.status.GeoLocation, ac.status.GeoISP = prev.GeoLocation, prev.GeoISP
	} else {
		ac.status.FirstSeen = now
	}
	if err := h.store.UpsertAgent(&ac.status); err != nil {
		h.log.Error("store agent", "err", err)
	}

	h.mu.Lock()
	old := h.agents[id]
	h.agents[id] = ac
	h.mu.Unlock()
	if old != nil {
		h.log.Info("agent reconnected, replacing previous connection", "agent", id)
		old.close()
	}
	h.log.Info("agent connected", "agent", id, "ip", remoteIP, "arch", hello.Arch, "version", hello.Version, "caps", strings.Join(hello.Capabilities, ","))

	welcome, _ := protocol.NewMessage(protocol.MsgWelcome, protocol.Welcome{AgentID: id, PublicIP: remoteIP, ServerTime: now.UnixMilli()})
	_ = conn.SetWriteDeadline(now.Add(10 * time.Second))
	if err := conn.WriteJSON(welcome); err != nil {
		h.unregister(ac)
		return
	}

	if h.geo != nil {
		go h.lookupAgentGeo(ac, remoteIP)
	}

	go h.writeLoop(ac)
	h.offerUpdate(ac, hello)
	h.readLoop(ac)
	h.unregister(ac)
}

// offerUpdate tells an agent to fetch the server's bundled build when the
// versions differ. Both sides must be real builds ("dev" never triggers).
func (h *Hub) offerUpdate(ac *agentConn, hello protocol.Hello) {
	if h.files == nil || !hello.SelfUpdate {
		return
	}
	sv := buildinfo.Version
	if sv == "" || sv == "dev" || hello.Version == "" || hello.Version == "dev" || hello.Version == sv {
		return
	}
	f := h.files.Lookup(hello.OS, hello.Arch, hello.Variant)
	if f == nil {
		h.log.Info("agent outdated but no bundled binary for its platform", "agent", ac.id, "os", hello.OS, "arch", hello.Arch, "variant", hello.Variant)
		return
	}
	msg, err := protocol.NewMessage(protocol.MsgUpdate, protocol.Update{Version: sv, Path: "/api/agent/download/" + f.Key, SHA256: f.SHA256, Size: f.Size})
	if err != nil {
		return
	}
	if ac.trySend(msg) {
		h.log.Info("offered self-update", "agent", ac.id, "from", hello.Version, "to", sv, "platform", f.Key)
	}
}

func (h *Hub) lookupAgentGeo(ac *agentConn, ip string) {
	loc, isp := h.geo.LookupAgent(ip)
	if loc == "" && isp == "" {
		return
	}
	ac.mu.Lock()
	ac.status.GeoLocation, ac.status.GeoISP = loc, isp
	st := ac.status
	ac.mu.Unlock()
	if err := h.store.UpsertAgent(&st); err != nil {
		h.log.Error("store agent geo", "err", err)
	}
}

func (h *Hub) writeLoop(ac *agentConn) {
	ping := time.NewTicker(agentPingInterval)
	defer ping.Stop()
	for {
		select {
		case <-ac.done:
			return
		case m := <-ac.send:
			_ = ac.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := ac.conn.WriteJSON(m); err != nil {
				ac.close()
				return
			}
		case <-ping.C:
			if err := ac.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				ac.close()
				return
			}
		}
	}
}

func (h *Hub) readLoop(ac *agentConn) {
	ac.conn.SetPongHandler(func(string) error { return ac.conn.SetReadDeadline(time.Now().Add(agentReadTimeout)) })
	for {
		_ = ac.conn.SetReadDeadline(time.Now().Add(agentReadTimeout))
		var m protocol.Message
		if err := ac.conn.ReadJSON(&m); err != nil {
			return
		}
		switch m.Type {
		case protocol.MsgProgress:
			var p protocol.Progress
			if json.Unmarshal(m.Payload, &p) == nil {
				h.handleProgress(ac, p)
			}
		case protocol.MsgResult:
			var r protocol.Result
			if json.Unmarshal(m.Payload, &r) == nil {
				h.handleResult(ac, r)
			}
		}
	}
}

func (h *Hub) unregister(ac *agentConn) {
	ac.close()
	h.mu.Lock()
	current := h.agents[ac.id] == ac
	if current {
		delete(h.agents, ac.id)
	}
	tasks := make([]*taskRun, 0, len(h.tasks))
	for _, tr := range h.tasks {
		tasks = append(tasks, tr)
	}
	h.mu.Unlock()
	for _, tr := range tasks {
		h.failAgentResult(tr, ac, "agent disconnected")
	}
	if current {
		h.log.Info("agent disconnected", "agent", ac.id)
	}
	if err := h.store.TouchAgent(ac.id, time.Now()); err != nil {
		h.log.Error("touch agent", "err", err)
	}
}

// Agents merges stored agents with live connection state.
func (h *Hub) Agents() []*protocol.AgentStatus {
	stored, err := h.store.ListAgents()
	if err != nil {
		h.log.Error("list agents", "err", err)
	}
	byID := map[string]*protocol.AgentStatus{}
	for _, a := range stored {
		byID[a.ID] = a
	}
	h.mu.RLock()
	for id, ac := range h.agents {
		s := ac.snapshot()
		if prev, ok := byID[id]; ok {
			s.FirstSeen = prev.FirstSeen
			if s.GeoLocation == "" {
				s.GeoLocation, s.GeoISP = prev.GeoLocation, prev.GeoISP
			}
		}
		byID[id] = &s
	}
	h.mu.RUnlock()
	out := make([]*protocol.AgentStatus, 0, len(byID))
	for _, a := range byID {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Online != out[j].Online {
			return out[i].Online
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// RemoveAgent deletes an offline agent's record.
func (h *Hub) RemoveAgent(id string) error {
	h.mu.RLock()
	_, online := h.agents[id]
	h.mu.RUnlock()
	if online {
		return errors.New("agent is online")
	}
	return h.store.DeleteAgent(id)
}

// --- tasks ------------------------------------------------------------------

type taskRun struct {
	task protocol.Task

	mu      sync.Mutex
	results map[string]*protocol.AgentResult
	order   []string
	conns   map[string]*agentConn
	subs    map[chan protocol.Event]struct{}
	pending int
	done    bool
	doneAt  time.Time
	timer   *time.Timer
}

func (tr *taskRun) snapshotLocked() protocol.Event {
	ev := protocol.Event{Type: "snapshot", Done: tr.done}
	t := tr.task
	ev.Task = &t
	for _, id := range tr.order {
		r := *tr.results[id]
		ev.Results = append(ev.Results, &r)
	}
	return ev
}

func (tr *taskRun) broadcastLocked(ev protocol.Event) {
	for ch := range tr.subs {
		select {
		case ch <- ev:
		default: // slow subscriber; it will catch up from the final result
		}
	}
}

// finishResultLocked moves one agent's result to a terminal state and emits
// the events. changed is false when it was already terminal; finished is true
// when this call completed the whole task.
func (tr *taskRun) finishResultLocked(res *protocol.AgentResult, status protocol.ResultStatus, errMsg string) (changed, finished bool) {
	if res.Status == protocol.StatusDone || res.Status == protocol.StatusError {
		return false, false
	}
	now := time.Now()
	res.Status = status
	res.Error = errMsg
	res.FinishedAt = &now
	res.Progress = nil
	if ac := tr.conns[res.AgentID]; ac != nil {
		ac.running.Add(-1)
		delete(tr.conns, res.AgentID)
	}
	tr.pending--
	copyRes := *res
	tr.broadcastLocked(protocol.Event{Type: "result", AgentID: res.AgentID, Result: &copyRes})
	if tr.pending <= 0 && !tr.done {
		tr.done = true
		tr.doneAt = now
		if tr.timer != nil {
			tr.timer.Stop()
		}
		tr.broadcastLocked(protocol.Event{Type: "done", Done: true})
		finished = true
	}
	return true, finished
}

func newTaskID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return strconv.FormatInt(time.Now().UnixMilli(), 36) + hex.EncodeToString(b[:])
}

// CreateTask dispatches a probe to the given agents (all online agents when
// agentIDs is empty) on behalf of owner and returns the initial snapshot.
func (h *Hub) CreateTask(typ protocol.TaskType, target string, params protocol.Params, agentIDs []string, owner string) (*protocol.Event, error) {
	return h.createTask(typ, target, params, agentIDs, "", owner)
}

// CreateTaskWithMonitor is CreateTask for a scheduled monitor run, which has
// no owner.
func (h *Hub) CreateTaskWithMonitor(typ protocol.TaskType, target string, params protocol.Params, agentIDs []string, monitorID string) (*protocol.Event, error) {
	return h.createTask(typ, target, params, agentIDs, monitorID, "")
}

func (h *Hub) createTask(typ protocol.TaskType, target string, params protocol.Params, agentIDs []string, monitorID, owner string) (*protocol.Event, error) {
	if !typ.Valid() {
		return nil, errors.New("invalid task type")
	}
	target = strings.TrimSpace(target)
	if target == "" || len(target) > 2048 {
		return nil, errors.New("invalid target")
	}

	h.mu.RLock()
	if len(agentIDs) == 0 {
		for id := range h.agents {
			agentIDs = append(agentIDs, id)
		}
		sort.Strings(agentIDs)
	}
	conns := map[string]*agentConn{}
	for _, id := range agentIDs {
		if ac, ok := h.agents[id]; ok {
			conns[id] = ac
		}
	}
	h.mu.RUnlock()
	if len(agentIDs) == 0 {
		return nil, errors.New("no agents online")
	}

	task := protocol.Task{ID: newTaskID(), Type: typ, Target: target, Params: params, CreatedAt: time.Now(), AgentIDs: agentIDs, MonitorID: monitorID, Owner: owner}
	tr := &taskRun{
		task:    task,
		results: map[string]*protocol.AgentResult{},
		conns:   map[string]*agentConn{},
		subs:    map[chan protocol.Event]struct{}{},
	}
	if err := h.store.InsertTask(&task); err != nil {
		return nil, err
	}

	agentTask := task
	agentTask.Owner = "" // who asked is none of the agents' business
	taskMsg, err := protocol.NewMessage(protocol.MsgTask, agentTask)
	if err != nil {
		return nil, err
	}

	tr.mu.Lock()
	for _, id := range agentIDs {
		if _, dup := tr.results[id]; dup {
			continue
		}
		res := &protocol.AgentResult{TaskID: task.ID, AgentID: id, Status: protocol.StatusPending}
		if ac := conns[id]; ac != nil {
			st := ac.snapshot()
			res.AgentName, res.Location, res.ISP = st.Name, st.Location, st.ISP
			if res.Location == "" {
				res.Location = st.GeoLocation
			}
			if res.ISP == "" {
				res.ISP = st.GeoISP
			}
		} else if st, err := h.store.GetAgent(id); err == nil {
			res.AgentName, res.Location, res.ISP = st.Name, st.Location, st.ISP
		} else {
			res.AgentName = id
		}
		tr.results[id] = res
		tr.order = append(tr.order, id)

		ac := conns[id]
		if ac == nil {
			res.Status, res.Error = protocol.StatusError, "agent offline"
			now := time.Now()
			res.FinishedAt = &now
			continue
		}
		if !ac.trySend(taskMsg) {
			res.Status, res.Error = protocol.StatusError, "agent send queue full"
			now := time.Now()
			res.FinishedAt = &now
			continue
		}
		now := time.Now()
		res.Status = protocol.StatusRunning
		res.StartedAt = &now
		tr.conns[id] = ac
		ac.running.Add(1)
		tr.pending++
	}
	if tr.pending == 0 {
		tr.done = true
		tr.doneAt = time.Now()
	} else {
		tr.timer = time.AfterFunc(h.cfg.TaskTimeout, func() { h.expireTask(tr) })
	}
	snap := tr.snapshotLocked()
	tr.mu.Unlock()

	h.mu.Lock()
	h.tasks[task.ID] = tr
	h.mu.Unlock()

	for _, r := range snap.Results {
		if r.Status == protocol.StatusError {
			if err := h.store.UpsertResult(r); err != nil {
				h.log.Error("store result", "err", err)
			}
		}
	}
	if monitorID == "" {
		h.log.Info("task created", "task", task.ID, "type", typ, "target", target, "agents", len(agentIDs), "dispatched", tr.pending)
	}
	if snap.Done {
		h.notifyDone(tr)
	}
	return &snap, nil
}

func (h *Hub) getTask(id string) *taskRun {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.tasks[id]
}

func (h *Hub) handleProgress(ac *agentConn, p protocol.Progress) {
	tr := h.getTask(p.TaskID)
	if tr == nil {
		return
	}
	if h.geo != nil && tr.task.Type == protocol.TaskMTR && p.Kind == "hops" {
		var hops []protocol.MTRHop
		if json.Unmarshal(p.Data, &hops) == nil {
			h.geo.AnnotateHops(hops)
			if b, err := json.Marshal(hops); err == nil {
				p.Data = b
			}
		}
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	res := tr.results[ac.id]
	if res == nil || res.Status != protocol.StatusRunning || tr.conns[ac.id] != ac {
		return
	}
	if len(res.Progress) < maxProgressEvents {
		res.Progress = append(res.Progress, p)
	}
	tr.broadcastLocked(protocol.Event{Type: "progress", AgentID: ac.id, Progress: &p})
}

func (h *Hub) handleResult(ac *agentConn, r protocol.Result) {
	tr := h.getTask(r.TaskID)
	if tr == nil {
		return
	}
	if h.geo != nil && tr.task.Type == protocol.TaskMTR && len(r.Data) > 0 {
		var mr protocol.MTRResult
		if json.Unmarshal(r.Data, &mr) == nil {
			h.geo.AnnotateHops(mr.Hops)
			if b, err := json.Marshal(mr); err == nil {
				r.Data = b
			}
		}
	}
	tr.mu.Lock()
	res := tr.results[ac.id]
	if res == nil || tr.conns[ac.id] != ac {
		tr.mu.Unlock()
		return
	}
	res.Data = r.Data
	res.DurationMs = r.DurationMs
	status := protocol.StatusDone
	if !r.OK {
		status = protocol.StatusError
	}
	changed, finished := tr.finishResultLocked(res, status, r.Error)
	copyRes := *res
	tr.mu.Unlock()
	if changed {
		if err := h.store.UpsertResult(&copyRes); err != nil {
			h.log.Error("store result", "err", err)
		}
	}
	if finished {
		h.notifyDone(tr)
	}
}

// failAgentResult marks ac's result on tr as failed (used on disconnect).
func (h *Hub) failAgentResult(tr *taskRun, ac *agentConn, reason string) {
	tr.mu.Lock()
	res := tr.results[ac.id]
	if res == nil || tr.conns[ac.id] != ac {
		tr.mu.Unlock()
		return
	}
	changed, finished := tr.finishResultLocked(res, protocol.StatusError, reason)
	copyRes := *res
	tr.mu.Unlock()
	if changed {
		_ = h.store.UpsertResult(&copyRes)
	}
	if finished {
		h.notifyDone(tr)
	}
}

// failAll terminates every non-final result on tr with reason and tells the
// agents to stop.
func (h *Hub) failAll(tr *taskRun, reason string) {
	cancelMsg, _ := protocol.NewMessage(protocol.MsgCancel, map[string]string{"task_id": tr.task.ID})
	tr.mu.Lock()
	var changed []*protocol.AgentResult
	finished := false
	for _, id := range tr.order {
		res := tr.results[id]
		if ac := tr.conns[id]; ac != nil {
			ac.trySend(cancelMsg)
		}
		if ch, fin := tr.finishResultLocked(res, protocol.StatusError, reason); ch {
			c := *res
			changed = append(changed, &c)
			finished = finished || fin
		}
	}
	tr.mu.Unlock()
	for _, r := range changed {
		_ = h.store.UpsertResult(r)
	}
	if finished {
		h.notifyDone(tr)
	}
}

func (h *Hub) expireTask(tr *taskRun) {
	h.failAll(tr, "timeout")
}

// CancelTask aborts a running task. Returns false if unknown.
func (h *Hub) CancelTask(id string) bool {
	tr := h.getTask(id)
	if tr == nil {
		return false
	}
	h.failAll(tr, "cancelled")
	return true
}

// Subscribe returns the current snapshot plus a channel of subsequent events.
// ok is false when the task is not in memory (finished long ago or unknown).
func (h *Hub) Subscribe(taskID string) (snapshot protocol.Event, events <-chan protocol.Event, unsubscribe func(), ok bool) {
	tr := h.getTask(taskID)
	if tr == nil {
		return protocol.Event{}, nil, nil, false
	}
	ch := make(chan protocol.Event, 2048)
	tr.mu.Lock()
	snap := tr.snapshotLocked()
	if !tr.done {
		tr.subs[ch] = struct{}{}
	}
	tr.mu.Unlock()
	unsub := func() {
		tr.mu.Lock()
		delete(tr.subs, ch)
		tr.mu.Unlock()
	}
	return snap, ch, unsub, true
}

// LoadTask returns a finished task from the store as a snapshot event.
func (h *Hub) LoadTask(taskID string) (*protocol.Event, error) {
	if tr := h.getTask(taskID); tr != nil {
		tr.mu.Lock()
		snap := tr.snapshotLocked()
		tr.mu.Unlock()
		return &snap, nil
	}
	task, err := h.store.GetTask(taskID)
	if err != nil {
		return nil, err
	}
	results, err := h.store.ListResults(taskID)
	if err != nil {
		return nil, err
	}
	return &protocol.Event{Type: "snapshot", Task: task, Results: results, Done: true}, nil
}

func (h *Hub) janitor() {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	lastPrune := time.Time{}
	for range tick.C {
		now := time.Now()
		h.mu.Lock()
		for id, tr := range h.tasks {
			tr.mu.Lock()
			expired := tr.done && now.Sub(tr.doneAt) > finishedTaskTTL
			tr.mu.Unlock()
			if expired {
				delete(h.tasks, id)
			}
		}
		h.mu.Unlock()
		if now.Sub(lastPrune) > time.Hour {
			lastPrune = now
			if h.cfg.RetainDays > 0 {
				cutoff := now.AddDate(0, 0, -h.cfg.RetainDays)
				if n, err := h.store.PruneTasks(cutoff); err != nil {
					h.log.Error("prune tasks", "err", err)
				} else if n > 0 {
					h.log.Info("pruned old tasks", "count", n)
				}
				if n, err := h.store.PruneSamples(cutoff); err != nil {
					h.log.Error("prune samples", "err", err)
				} else if n > 0 {
					h.log.Info("pruned old samples", "count", n)
				}
			}
			// Per-run details of scheduled monitors are bulky; keep them briefly.
			if n, err := h.store.PruneMonitorTasks(now.Add(-h.cfg.MonitorTaskRetain)); err != nil {
				h.log.Error("prune monitor tasks", "err", err)
			} else if n > 0 {
				h.log.Info("pruned monitor run details", "count", n)
			}
		}
	}
}
