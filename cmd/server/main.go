// Command probe-server is the probe-platform control plane and dashboard.
//
// Configuration via flags or PROBE_* environment variables; see --help.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"probe-platform/internal/buildinfo"
	"probe-platform/internal/server"
	"probe-platform/web"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		fmt.Println("probe-server", buildinfo.Version)
		return
	}
	cfg := server.LoadConfig(os.Args[1:])
	log := newLogger(cfg.LogLevel)

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Error("create data dir", "err", err)
		os.Exit(1)
	}
	// Nodes have their own tokens, created on the dashboard. The shared token
	// of earlier versions is only read (never generated) so that nodes
	// installed with it keep working until they have been migrated.
	if cfg.AgentToken == "" {
		if b, err := os.ReadFile(filepath.Join(cfg.DataDir, "agent_token")); err == nil {
			cfg.AgentToken = strings.TrimSpace(string(b))
		}
	}

	store, err := server.OpenStore(filepath.Join(cfg.DataDir, "probe.db"))
	if err != nil {
		log.Error("open store", "err", err)
		os.Exit(1)
	}
	defer store.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	geo := server.NewGeoIP(cfg, log)
	files := server.NewAgentFiles(cfg.AgentsDir, log)
	hub := server.NewHub(cfg, store, geo, files, log)
	settings, err := server.LoadSettings(store, cfg, log)
	if err != nil {
		log.Error("load settings", "err", err)
		os.Exit(1)
	}
	switch settings.LegacyTokenState() {
	case "enabled":
		log.Info("legacy shared agent token accepted for nodes not migrated yet",
			"hint", "nodes switch to their own token automatically; disable the shared token on the Agents page once all have migrated")
	case "disabled":
		log.Info("legacy shared agent token configured but disabled; PROBE_AGENT_TOKEN / data/agent_token can be removed")
	}
	if !settings.LoginEnabled() {
		log.Warn("no admin login configured: the dashboard is OPEN to anyone who can reach it. Set PROBE_ADMIN_PASSWORD or configure Logto on the settings page.")
	}
	notifier := server.NewNotifier(log)
	sched := server.NewScheduler(store, hub, notifier, settings.BaseURL, log)
	go sched.Run(ctx)
	handler := server.NewHandler(cfg, hub, store, web.Dist(), settings, sched, notifier, log)

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout: SSE and WebSocket connections are long-lived.
	}
	go func() {
		log.Info("probe-server listening", "addr", cfg.Listen, "version", buildinfo.Version, "data", cfg.DataDir)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}
