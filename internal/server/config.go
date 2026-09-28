// Package server implements the probe-platform control plane: agent hub,
// task dispatch, persistence and the HTTP/SSE API used by the dashboard.
package server

import (
	"flag"
	"os"
	"strconv"
	"time"
)

// Config is the server configuration. Every field maps to a flag and an
// environment variable (PROBE_*).
type Config struct {
	Listen        string
	DataDir       string
	AgentToken    string
	AdminPassword string
	TrustProxy    bool          // honour X-Forwarded-For / X-Real-IP
	GeoIPOnline   bool          // look up agent public IPs via ip-api.com
	IP2RegionDB   string        // path to ip2region xdb for offline hop annotation
	TaskTimeout   time.Duration // hard cap for a task across all agents
	RetainDays    int           // history retention; 0 keeps forever
	LogLevel      string
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func envInt(key string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return n
	}
	return def
}

// LoadConfig parses flags (with env fallbacks) from args.
func LoadConfig(args []string) Config {
	fs := flag.NewFlagSet("probe-server", flag.ExitOnError)
	var c Config
	fs.StringVar(&c.Listen, "listen", envOr("PROBE_LISTEN", ":8080"), "listen address")
	fs.StringVar(&c.DataDir, "data", envOr("PROBE_DATA_DIR", "./data"), "data directory (sqlite db, token file, geoip db)")
	fs.StringVar(&c.AgentToken, "agent-token", envOr("PROBE_AGENT_TOKEN", ""), "shared secret agents authenticate with (generated and stored in data dir when empty)")
	fs.StringVar(&c.AdminPassword, "admin-password", envOr("PROBE_ADMIN_PASSWORD", ""), "dashboard password (empty = no login)")
	fs.BoolVar(&c.TrustProxy, "trust-proxy", envBool("PROBE_TRUST_PROXY", false), "trust X-Forwarded-For (set when behind nginx/caddy)")
	fs.BoolVar(&c.GeoIPOnline, "geoip-online", envBool("PROBE_GEOIP_ONLINE", true), "look up agent public IP location via ip-api.com")
	fs.StringVar(&c.IP2RegionDB, "ip2region-db", envOr("PROBE_IP2REGION_DB", ""), "path to ip2region xdb (default <data>/ip2region.xdb if present)")
	timeout := fs.Int("task-timeout", envInt("PROBE_TASK_TIMEOUT", 180), "seconds before an unfinished task is failed")
	fs.IntVar(&c.RetainDays, "retain-days", envInt("PROBE_RETAIN_DAYS", 90), "delete task history older than this many days (0 = keep)")
	fs.StringVar(&c.LogLevel, "log-level", envOr("PROBE_LOG_LEVEL", "info"), "debug|info|warn|error")
	_ = fs.Parse(args)
	c.TaskTimeout = time.Duration(*timeout) * time.Second
	return c
}
