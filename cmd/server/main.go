// Command probe-server is the probe-platform control plane and dashboard.
//
// Configuration via flags or PROBE_* environment variables; see --help.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	if cfg.AgentToken == "" {
		tok, generated, err := loadOrCreateToken(filepath.Join(cfg.DataDir, "agent_token"))
		if err != nil {
			log.Error("agent token", "err", err)
			os.Exit(1)
		}
		cfg.AgentToken = tok
		if generated {
			log.Info("generated agent token (also saved to data dir)", "token", tok, "file", filepath.Join(cfg.DataDir, "agent_token"))
		} else {
			log.Info("agent token loaded", "file", filepath.Join(cfg.DataDir, "agent_token"))
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

func loadOrCreateToken(path string) (string, bool, error) {
	if b, err := os.ReadFile(path); err == nil {
		if tok := strings.TrimSpace(string(b)); tok != "" {
			return tok, false, nil
		}
	}
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", false, err
	}
	tok := hex.EncodeToString(raw[:])
	if err := os.WriteFile(path, []byte(tok+"\n"), 0o600); err != nil {
		return "", false, err
	}
	return tok, true, nil
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
