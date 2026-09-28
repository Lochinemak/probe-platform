package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"probe-platform/internal/buildinfo"
	"probe-platform/internal/protocol"
)

// API is the HTTP surface: dashboard JSON/SSE endpoints, agent WebSocket and
// the embedded single-page app.
type API struct {
	cfg    Config
	hub    *Hub
	store  *Store
	auth   *sessionAuth
	static fs.FS
	files  *AgentFiles
	log    *slog.Logger

	sched    *Scheduler // nil in tests / when disabled
	notifier *Notifier
	settings *Settings
}

// NewHandler wires every route.
func NewHandler(cfg Config, hub *Hub, store *Store, static fs.FS, settings *Settings, sched *Scheduler, notifier *Notifier, log *slog.Logger) http.Handler {
	a := &API{cfg: cfg, hub: hub, store: store, auth: newSessionAuth(settings), static: static, files: hub.files, log: log, sched: sched, notifier: notifier, settings: settings}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/agent", hub.HandleAgentWS)
	mux.HandleFunc("GET /api/health", a.health)
	mux.HandleFunc("GET /api/session", a.session)
	mux.HandleFunc("POST /api/login", a.login)
	mux.HandleFunc("POST /api/logout", a.logout)
	mux.HandleFunc("GET /api/auth/logto/login", a.logtoLogin)
	mux.HandleFunc("GET /api/auth/logto/callback", a.logtoCallback)
	// Guests (when enabled) may probe and read results; node details are trimmed.
	mux.Handle("GET /api/agents", a.protectGuest(a.listAgents))
	mux.Handle("DELETE /api/agents/{id}", a.protect(a.deleteAgent))
	mux.Handle("GET /api/tasks", a.protectGuest(a.listTasks))
	mux.Handle("POST /api/tasks", a.protectGuest(a.createTask))
	mux.Handle("GET /api/tasks/{id}", a.protectGuest(a.getTask))
	mux.Handle("GET /api/tasks/{id}/events", a.protectGuest(a.taskEvents))
	mux.Handle("POST /api/tasks/{id}/cancel", a.protectGuest(a.cancelTask))
	mux.Handle("GET /api/monitors", a.protect(a.listMonitors))
	mux.Handle("POST /api/monitors", a.protect(a.createMonitor))
	mux.Handle("GET /api/monitors/{id}", a.protect(a.getMonitor))
	mux.Handle("PUT /api/monitors/{id}", a.protect(a.updateMonitor))
	mux.Handle("DELETE /api/monitors/{id}", a.protect(a.deleteMonitor))
	mux.Handle("POST /api/monitors/{id}/run", a.protect(a.runMonitor))
	mux.Handle("GET /api/monitors/{id}/series", a.protect(a.monitorSeries))
	mux.Handle("GET /api/monitors/{id}/alerts", a.protect(a.monitorAlerts))
	mux.Handle("GET /api/alerts", a.protect(a.listAlerts))
	mux.Handle("GET /api/notify", a.protect(a.listChannels))
	mux.Handle("POST /api/notify", a.protect(a.createChannel))
	mux.Handle("PUT /api/notify/{id}", a.protect(a.updateChannel))
	mux.Handle("DELETE /api/notify/{id}", a.protect(a.deleteChannel))
	mux.Handle("POST /api/notify/{id}/test", a.protect(a.testChannel))
	mux.Handle("GET /api/settings", a.protect(a.getSettings))
	mux.Handle("PUT /api/settings", a.protect(a.updateSettings))
	mux.Handle("POST /api/settings/password", a.protect(a.changePassword))
	mux.Handle("POST /api/settings/logto/test", a.protect(a.testLogto))
	mux.Handle("GET /api/agent/token", a.protect(a.agentToken))
	mux.Handle("GET /api/agent/version", a.protectAgentOrSession(a.agentVersion))
	mux.Handle("GET /api/agent/download/{key}", a.protectAgentOrSession(a.agentDownload))
	mux.HandleFunc("GET /install-agent.sh", a.installScript)
	mux.HandleFunc("GET /install-agent-openwrt.sh", a.installScript)
	mux.HandleFunc("GET /uninstall-agent.sh", a.installScript)
	mux.HandleFunc("GET /install-agent.ps1", a.installScript)
	mux.Handle("/", a.spaHandler())
	return a.recoverer(a.securityHeaders(a.crossSiteGuard(mux)))
}

// securityHeaders sets the response headers that keep a browser from
// reinterpreting our responses: no sniffing, no framing, no referrer leakage,
// and a content policy for the dashboard itself (the built SPA loads one
// external module script and no inline script).
func (a *API) securityHeaders(next http.Handler) http.Handler {
	const csp = "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; " +
		"connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; object-src 'none'; form-action 'self'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		if !strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/ws/") {
			h.Set("Content-Security-Policy", csp)
		}
		if secureCookie(r) {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

// crossSiteGuard is defence in depth against cross-site requests riding on the
// session cookie. SameSite=Lax already keeps the cookie off cross-site form
// posts; this refuses the request outright when the browser tells us it is
// cross-site. Non-browser clients (curl, the install scripts, agents) send
// neither header and are unaffected.
func (a *API) crossSiteGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			switch r.Header.Get("Sec-Fetch-Site") {
			case "", "same-origin", "none":
			default:
				writeErr(w, http.StatusForbidden, "拒绝跨站请求")
				return
			}
			if o := r.Header.Get("Origin"); o != "" {
				u, err := url.Parse(o)
				if err != nil || !strings.EqualFold(u.Host, r.Host) {
					writeErr(w, http.StatusForbidden, "拒绝跨站请求")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (a *API) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				a.log.Error("panic", "err", rec, "path", r.URL.Path)
				writeErr(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// protect requires an admin session (or open mode).
func (a *API) protect(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.auth.isAdmin(r) {
			writeErr(w, http.StatusUnauthorized, "需要管理员登录")
			return
		}
		next(w, r)
	})
}

// protectGuest allows admins and, when guest access is on, anonymous guests.
func (a *API) protectGuest(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.auth.role(r) == "" {
			writeErr(w, http.StatusUnauthorized, "需要登录")
			return
		}
		next(w, r)
	})
}

// guestLimiter caps how many tasks an anonymous IP may create per minute.
var guestLimiter = &sessionAuth{attempts: map[string][]time.Time{}}

// guestParams trims probe parameters for anonymous visitors so a public
// dashboard cannot be used to generate heavy traffic from every node, nor to
// reach anything but the public internet. Whatever the request asked for, the
// fields set here win: they are the guest policy, not a default.
func guestParams(typ protocol.TaskType, p protocol.Params) protocol.Params {
	// The agents run inside home LANs and on the dashboard host itself, so an
	// untrusted caller is confined to globally routable destinations.
	p.PublicOnly = true
	p.InsecureTLS = false
	switch typ {
	case protocol.TaskHTTP:
		if p.Count > 3 {
			p.Count = 3
		}
		p.SpeedTest = false
		p.SpeedSeconds = 0
		// A crafted method, headers or body would let an anonymous visitor send
		// arbitrary requests from every node; a guest gets a plain read.
		if strings.EqualFold(strings.TrimSpace(p.Method), "HEAD") {
			p.Method = "HEAD"
		} else {
			p.Method = "GET"
		}
		p.Headers = nil
		p.Body = ""
	case protocol.TaskMTR:
		if p.Count > 10 {
			p.Count = 10
		}
	case protocol.TaskDNS:
		if p.Count > 5 {
			p.Count = 5
		}
	default:
		if p.Count > 20 {
			p.Count = 20
		}
	}
	if p.IntervalMs > 0 && p.IntervalMs < 200 {
		p.IntervalMs = 200
	}
	return p
}

// publicAgent trims an agent record to what a guest may see.
func publicAgent(ag *protocol.AgentStatus) *protocol.AgentStatus {
	loc := ag.Location
	if loc == "" {
		loc = ag.GeoLocation
	}
	isp := ag.ISP
	if isp == "" {
		isp = ag.GeoISP
	}
	return &protocol.AgentStatus{ID: ag.ID, Name: ag.Name, Online: ag.Online, Location: loc, ISP: isp, Capabilities: ag.Capabilities, Running: ag.Running}
}

// hasAgentToken accepts the shared agent secret as a bearer token.
func (a *API) hasAgentToken(r *http.Request) bool {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") || a.cfg.AgentToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(h, "Bearer ")), []byte(a.cfg.AgentToken)) == 1
}

// protectAgentOrSession allows either a logged-in dashboard user or an agent.
func (a *API) protectAgentOrSession(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.hasAgentToken(r) && !a.auth.isAdmin(r) {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	})
}

var agentKeyRe = regexp.MustCompile(`^[a-z0-9]+-[a-z0-9]+$`)

// agentToken reveals the shared agent secret to a logged-in dashboard user so
// the onboarding snippets can be copied ready to run.
func (a *API) agentToken(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"token": a.cfg.AgentToken})
}

// agentVersion lists the agent binaries bundled with this server.
func (a *API) agentVersion(w http.ResponseWriter, _ *http.Request) {
	files := []*AgentFile{}
	if a.files != nil {
		files = a.files.List()
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": buildinfo.Version, "files": files})
}

// agentDownload streams one agent binary, e.g. /api/agent/download/linux-arm64.
func (a *API) agentDownload(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if a.files == nil || !agentKeyRe.MatchString(key) {
		writeErr(w, http.StatusNotFound, "no agent binaries available")
		return
	}
	af := a.files.ByKey(key)
	if af == nil {
		writeErr(w, http.StatusNotFound, "no agent binary for "+key)
		return
	}
	fh, err := a.files.Open(af)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer fh.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+af.Name+"\"")
	w.Header().Set("X-Checksum-Sha256", af.SHA256)
	w.Header().Set("X-Agent-Version", buildinfo.Version)
	http.ServeContent(w, r, af.Name, af.modTime, fh)
}

// installScript serves the deploy/*-agent* helper scripts from the agents
// dir so a node can be onboarded with `curl .../install-agent.sh | sudo sh`
// (Windows: `irm .../install-agent.ps1 | iex`) and removed again with
// `curl .../uninstall-agent.sh | sudo sh`.
func (a *API) installScript(w http.ResponseWriter, r *http.Request) {
	if a.files == nil {
		http.Error(w, "not available", http.StatusNotFound)
		return
	}
	name := path.Base(r.URL.Path) // install-agent.sh, install-agent-openwrt.sh, uninstall-agent.sh or install-agent.ps1, fixed by the mux patterns
	b, err := os.ReadFile(filepath.Join(a.files.Dir(), name))
	if err != nil {
		http.Error(w, "not available", http.StatusNotFound)
		return
	}
	ct := "text/x-shellscript; charset=utf-8"
	if strings.HasSuffix(name, ".ps1") {
		// PowerShell's Invoke-RestMethod decodes by the declared charset and
		// assumes ISO-8859-1 without one.
		ct = "text/plain; charset=utf-8"
	}
	w.Header().Set("Content-Type", ct)
	_, _ = w.Write(b)
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"ok": true}
	if a.auth.isAdmin(r) {
		out["version"] = buildinfo.Version
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) session(w http.ResponseWriter, r *http.Request) {
	role := a.auth.role(r)
	var user map[string]string
	if sess := a.auth.sessionFrom(r); sess != nil {
		user = map[string]string{"name": sess.Name, "via": sess.Via}
	} else if !a.auth.enabled() {
		user = map[string]string{"name": "admin", "via": "open"}
	}
	out := map[string]any{
		"auth_required": a.auth.enabled(),
		"authenticated": role == roleAdmin,
		"role":          role,
		"user":          user,
		"guest_enabled": a.auth.guestAccess(),
		"login": map[string]bool{
			"password": a.auth.passwordLoginEnabled(),
			"logto":    a.settings.Logto() != nil,
		},
	}
	// Version and onboarding details are for administrators; a guest has no use
	// for them and they only help someone fingerprinting the build.
	if role == roleAdmin {
		out["version"] = buildinfo.Version
		out["agent_image"] = a.settings.AgentImage()
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	if !a.auth.enabled() {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	ip := a.cfg.ClientIP(r)
	if !a.auth.allowAttempt(ip) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts, try again in a minute")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if !a.auth.checkPassword(body.Username, body.Password) {
		a.log.Warn("failed login", "ip", ip, "user", body.Username)
		writeErr(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	a.auth.setCookie(w, r, Session{Role: roleAdmin, Name: a.settings.AdminUser(), Via: "password"})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- settings ---------------------------------------------------------------

func (a *API) getSettings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.settings.View())
}

func (a *API) updateSettings(w http.ResponseWriter, r *http.Request) {
	var p SettingsPatch
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&p); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	wasOpen := !a.auth.enabled()
	if err := a.settings.Update(p); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// Enabling the first login method from open mode would otherwise turn the
	// person doing it into a guest mid-setup; keep them signed in as admin.
	if wasOpen && a.auth.enabled() && a.auth.sessionFrom(r) == nil {
		a.auth.setCookie(w, r, Session{Role: roleAdmin, Name: a.settings.AdminUser(), Via: "bootstrap"})
	}
	a.log.Info("settings updated", "by", a.cfg.ClientIP(r))
	writeJSON(w, http.StatusOK, a.settings.View())
}

func (a *API) changePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	// A password-holder must prove the current one; a Logto-only admin may set the first password.
	if a.settings.PasswordLoginEnabled() && !a.settings.CheckPassword(a.settings.AdminUser(), body.Current) {
		writeErr(w, http.StatusUnauthorized, "当前密码不正确")
		return
	}
	if err := a.settings.SetPassword(body.New); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.auth.clearCookie(w)
	a.log.Info("admin password changed", "by", a.cfg.ClientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "relogin": true})
}

// testLogto runs OIDC discovery for the saved Logto settings.
func (a *API) testLogto(w http.ResponseWriter, r *http.Request) {
	l := a.settings.Logto()
	if l == nil {
		writeErr(w, http.StatusBadRequest, "请先保存 Logto 地址、App ID 和站点地址")
		return
	}
	if _, err := l.LoginURL(r.Context(), "test", "test-verifier-test-verifier-test-verifier-0000"); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "issuer": l.issuer, "redirect_url": l.RedirectURL()})
}

// logtoLogin starts the OIDC authorization code flow.
func (a *API) logtoLogin(w http.ResponseWriter, r *http.Request) {
	logto := a.settings.Logto()
	if logto == nil {
		writeErr(w, http.StatusNotFound, "logto login not configured")
		return
	}
	state := randomHex(16)
	verifier := randomHex(32)
	u, err := logto.LoginURL(r.Context(), state, verifier)
	if err != nil {
		a.log.Error("logto login", "err", err)
		http.Redirect(w, r, "/?login_error="+url.QueryEscape("Logto 不可用："+err.Error()), http.StatusFound)
		return
	}
	a.auth.setOIDCCookie(w, r, oidcState{State: state, Verifier: verifier})
	http.Redirect(w, r, u, http.StatusFound)
}

// logtoCallback finishes the flow and issues an admin session.
func (a *API) logtoCallback(w http.ResponseWriter, r *http.Request) {
	fail := func(msg string) {
		a.log.Warn("logto callback rejected", "reason", msg, "ip", a.cfg.ClientIP(r))
		http.Redirect(w, r, "/?login_error="+url.QueryEscape(msg), http.StatusFound)
	}
	logto := a.settings.Logto()
	if logto == nil {
		fail("Logto 登录未配置")
		return
	}
	st, ok := a.auth.takeOIDCCookie(w, r)
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		fail("Logto 返回错误：" + e + " " + q.Get("error_description"))
		return
	}
	if !ok || q.Get("state") == "" || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(st.State)) != 1 {
		fail("登录状态已过期或不匹配，请重试")
		return
	}
	id, err := logto.Exchange(r.Context(), q.Get("code"), st.Verifier)
	if err != nil {
		fail("换取令牌失败：" + err.Error())
		return
	}
	if !logto.IsAdmin(id) {
		if !logto.HasAdmins() {
			fail("尚未配置 Logto 管理员名单，所有 Logto 登录都会被拒绝；请先在「设置 → Logto 登录」里填写允许的管理员")
			return
		}
		fail("账号 " + id.DisplayName() + " 不在管理员名单中")
		return
	}
	a.auth.setCookie(w, r, Session{Role: roleAdmin, Name: id.DisplayName(), Via: "logto"})
	a.log.Info("logto login", "user", id.DisplayName(), "sub", id.Sub)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (a *API) logout(w http.ResponseWriter, _ *http.Request) {
	a.auth.clearCookie(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) listAgents(w http.ResponseWriter, r *http.Request) {
	agents := a.hub.Agents()
	if a.auth.role(r) != roleAdmin {
		trimmed := make([]*protocol.AgentStatus, 0, len(agents))
		for _, ag := range agents {
			trimmed = append(trimmed, publicAgent(ag))
		}
		agents = trimmed
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": agents})
}

func (a *API) deleteAgent(w http.ResponseWriter, r *http.Request) {
	if err := a.hub.RemoveAgent(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type createTaskReq struct {
	Type     protocol.TaskType `json:"type"`
	Target   string            `json:"target"`
	Params   protocol.Params   `json:"params"`
	AgentIDs []string          `json:"agent_ids"`
}

func (a *API) createTask(w http.ResponseWriter, r *http.Request) {
	var req createTaskReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if !req.Type.Valid() {
		writeErr(w, http.StatusBadRequest, "type must be one of ping, tcping, http, mtr")
		return
	}
	if strings.TrimSpace(req.Target) == "" {
		writeErr(w, http.StatusBadRequest, "target is required")
		return
	}
	if len(req.AgentIDs) > 500 {
		writeErr(w, http.StatusBadRequest, "too many agents")
		return
	}
	owner := a.auth.owner(r)
	if a.auth.role(r) == roleGuest {
		if !guestLimiter.allowAttempt(a.cfg.ClientIP(r)) {
			writeErr(w, http.StatusTooManyRequests, "游客每分钟最多发起 10 次拨测，请稍后再试或登录")
			return
		}
		req.Params = guestParams(req.Type, req.Params)
		// Rejected here as well as on the agent: this catches the target before
		// any packet is sent, including by an agent too old to know the policy.
		if err := validateGuestTarget(r.Context(), req.Type, req.Target, req.Params); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		// The cookie has to exist before the browser opens the event stream,
		// so mint it here rather than on the first page load.
		owner = a.auth.ensureGuestOwner(w, r)
	} else {
		// Only the server decides this; never take it from the request body.
		req.Params.PublicOnly = false
	}
	snap, err := a.hub.CreateTask(req.Type, req.Target, req.Params, req.AgentIDs, owner)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, snap)
}

// canSeeTask reports whether the request may read task: administrators see
// everything, guests only what their own browser started.
func (a *API) canSeeTask(r *http.Request, task *protocol.Task) bool {
	if a.auth.isAdmin(r) {
		return true
	}
	owner := a.auth.owner(r)
	return owner != "" && task.Owner == owner
}

func (a *API) listTasks(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	filter := TaskFilter{MonitorID: r.URL.Query().Get("monitor")}
	if !a.auth.isAdmin(r) {
		// Guests: only their own ad-hoc probes. A visitor without a guest
		// cookie has not started anything yet.
		filter.Owner = a.auth.owner(r)
		filter.MonitorID = ""
		if filter.Owner == "" {
			writeJSON(w, http.StatusOK, map[string]any{"tasks": []*protocol.Task{}})
			return
		}
	}
	tasks, err := a.store.ListTasks(limit, offset, filter)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

func (a *API) getTask(w http.ResponseWriter, r *http.Request) {
	snap, err := a.hub.LoadTask(r.PathValue("id"))
	if errors.Is(err, ErrNotFound) || (err == nil && !a.canSeeTask(r, snap.Task)) {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (a *API) cancelTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if snap, err := a.hub.LoadTask(id); err != nil || !a.canSeeTask(r, snap.Task) {
		writeErr(w, http.StatusNotFound, "task not running")
		return
	}
	if !a.hub.CancelTask(id) {
		writeErr(w, http.StatusNotFound, "task not running")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// taskEvents streams a task's lifecycle over Server-Sent Events: one snapshot,
// then progress/result events, then done.
func (a *API) taskEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	send := func(ev protocol.Event) error {
		b, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, b); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	headers := func() {
		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("Connection", "keep-alive")
		h.Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
	}

	snap, events, unsub, live := a.hub.Subscribe(id)
	if !live {
		loaded, err := a.hub.LoadTask(id)
		if errors.Is(err, ErrNotFound) || (err == nil && !a.canSeeTask(r, loaded.Task)) {
			writeErr(w, http.StatusNotFound, "task not found")
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		headers()
		_ = send(*loaded)
		_ = send(protocol.Event{Type: "done", Done: true})
		return
	}
	defer unsub()
	if !a.canSeeTask(r, snap.Task) {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	headers()
	if err := send(snap); err != nil {
		return
	}
	if snap.Done {
		_ = send(protocol.Event{Type: "done", Done: true})
		return
	}
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-events:
			if err := send(ev); err != nil {
				return
			}
			if ev.Type == "done" {
				return
			}
		case <-keepalive.C:
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// spaHandler serves the embedded frontend with history-API fallback.
func (a *API) spaHandler() http.Handler {
	fileServer := http.FileServer(http.FS(a.static))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/ws/") {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" && p != "index.html" {
			if f, err := a.static.Open(p); err == nil {
				st, statErr := f.Stat()
				f.Close()
				if statErr == nil && !st.IsDir() {
					if strings.HasPrefix(p, "assets/") {
						w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
					}
					fileServer.ServeHTTP(w, r)
					return
				}
			}
		}
		index, err := fs.ReadFile(a.static, "index.html")
		if err != nil {
			http.Error(w, "frontend not built: run `make web`", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}
