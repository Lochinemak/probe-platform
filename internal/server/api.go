package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
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
	log    *slog.Logger
}

// NewHandler wires every route.
func NewHandler(cfg Config, hub *Hub, store *Store, static fs.FS, log *slog.Logger) http.Handler {
	a := &API{cfg: cfg, hub: hub, store: store, auth: newSessionAuth(cfg.AdminPassword), static: static, log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/agent", hub.HandleAgentWS)
	mux.HandleFunc("GET /api/health", a.health)
	mux.HandleFunc("GET /api/session", a.session)
	mux.HandleFunc("POST /api/login", a.login)
	mux.HandleFunc("POST /api/logout", a.logout)
	mux.Handle("GET /api/agents", a.protect(a.listAgents))
	mux.Handle("DELETE /api/agents/{id}", a.protect(a.deleteAgent))
	mux.Handle("GET /api/tasks", a.protect(a.listTasks))
	mux.Handle("POST /api/tasks", a.protect(a.createTask))
	mux.Handle("GET /api/tasks/{id}", a.protect(a.getTask))
	mux.Handle("GET /api/tasks/{id}/events", a.protect(a.taskEvents))
	mux.Handle("POST /api/tasks/{id}/cancel", a.protect(a.cancelTask))
	mux.Handle("/", a.spaHandler())
	return a.recoverer(mux)
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

func (a *API) protect(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.auth.authenticated(r) {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	})
}

func (a *API) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": buildinfo.Version})
}

func (a *API) session(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"auth_required": a.auth.enabled,
		"authenticated": a.auth.authenticated(r),
		"version":       buildinfo.Version,
	})
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	if !a.auth.enabled {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	ip := clientIP(r, a.cfg.TrustProxy)
	if !a.auth.allowAttempt(ip) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts, try again in a minute")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if !a.auth.checkPassword(body.Password) {
		a.log.Warn("failed login", "ip", ip)
		writeErr(w, http.StatusUnauthorized, "wrong password")
		return
	}
	a.auth.setCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) logout(w http.ResponseWriter, _ *http.Request) {
	a.auth.clearCookie(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) listAgents(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"agents": a.hub.Agents()})
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
	snap, err := a.hub.CreateTask(req.Type, req.Target, req.Params, req.AgentIDs)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, snap)
}

func (a *API) listTasks(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	tasks, err := a.store.ListTasks(limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

func (a *API) getTask(w http.ResponseWriter, r *http.Request) {
	snap, err := a.hub.LoadTask(r.PathValue("id"))
	if errors.Is(err, ErrNotFound) {
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
	if !a.hub.CancelTask(r.PathValue("id")) {
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
		if errors.Is(err, ErrNotFound) {
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
