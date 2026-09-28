// Package server implements the probe-platform control plane: agent hub,
// task dispatch, persistence and the HTTP/SSE API used by the dashboard.
package server

import (
	"flag"
	"net/http"
	"os"
	"strconv"
	"time"
)

// ClientIP identifies the caller of r under this configuration. Used for rate
// limiting and audit logging, so it must not be forgeable by the caller.
func (c Config) ClientIP(r *http.Request) string {
	return clientIP(r, c.TrustProxy, c.ProxyHops)
}

// Config is the server configuration. Every field maps to a flag and an
// environment variable (PROBE_*).
type Config struct {
	Listen            string
	DataDir           string
	AgentToken        string // legacy shared agent token; nodes now have their own
	AdminPassword     string
	AdminUser         string // username for password login (default admin)
	GuestAccess       bool   // anonymous visitors may run probes (limited view)
	LogtoEndpoint     string // https://auth.example.com (the /oidc suffix is added)
	LogtoAppID        string
	LogtoAppSecret    string
	LogtoAdmins       string        // comma-separated subs/emails/usernames allowed as admin; empty = any
	TrustProxy        bool          // honour X-Forwarded-For / X-Real-IP
	ProxyHops         int           // number of reverse proxies in front; only the entries they appended to X-Forwarded-For are trusted
	GeoIPOnline       bool          // look up agent public IPs via ip-api.com
	IP2RegionDB       string        // path to ip2region xdb for offline hop annotation
	TaskTimeout       time.Duration // hard cap for a task across all agents
	RetainDays        int           // history retention; 0 keeps forever
	LogLevel          string
	AgentImage        string        // agent docker image shown on the dashboard's onboarding page
	AgentsDir         string        // directory with probe-agent-<os>-<arch> binaries served for self-update
	BaseURL           string        // public dashboard URL, used for links in notifications
	MonitorTaskRetain time.Duration // how long to keep per-run details of scheduled monitors
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
	fs.StringVar(&c.DataDir, "data", envOr("PROBE_DATA_DIR", "./data"), "data directory (sqlite db, geoip db)")
	fs.StringVar(&c.AgentToken, "agent-token", envOr("PROBE_AGENT_TOKEN", ""), "legacy shared agent token, only for migrating nodes installed before per-node tokens (default: <data>/agent_token if that file exists)")
	fs.StringVar(&c.AdminPassword, "admin-password", envOr("PROBE_ADMIN_PASSWORD", ""), "dashboard password (empty = no login)")
	fs.StringVar(&c.AdminUser, "admin-user", envOr("PROBE_ADMIN_USER", "admin"), "username for password login")
	fs.BoolVar(&c.GuestAccess, "guest", envBool("PROBE_GUEST", true), "let anonymous visitors run probes and view results (node details hidden)")
	fs.StringVar(&c.LogtoEndpoint, "logto-endpoint", envOr("PROBE_LOGTO_ENDPOINT", ""), "Logto (OIDC) endpoint, e.g. https://auth.example.com")
	fs.StringVar(&c.LogtoAppID, "logto-app-id", envOr("PROBE_LOGTO_APP_ID", ""), "Logto application id")
	fs.StringVar(&c.LogtoAppSecret, "logto-app-secret", envOr("PROBE_LOGTO_APP_SECRET", ""), "Logto application secret (traditional web app); empty for a public app with PKCE only")
	fs.StringVar(&c.LogtoAdmins, "logto-admins", envOr("PROBE_LOGTO_ADMINS", ""), "comma-separated Logto users (sub, email or username) allowed as admin; empty = every Logto user")
	fs.BoolVar(&c.TrustProxy, "trust-proxy", envBool("PROBE_TRUST_PROXY", false), "trust X-Forwarded-For (set when behind nginx/caddy)")
	fs.IntVar(&c.ProxyHops, "proxy-hops", envInt("PROBE_PROXY_HOPS", 1), "how many reverse proxies sit in front; the client's own X-Forwarded-For entries are ignored")
	fs.BoolVar(&c.GeoIPOnline, "geoip-online", envBool("PROBE_GEOIP_ONLINE", true), "look up agent public IP location via ip-api.com")
	fs.StringVar(&c.IP2RegionDB, "ip2region-db", envOr("PROBE_IP2REGION_DB", ""), "path to ip2region xdb (default <data>/ip2region.xdb if present)")
	timeout := fs.Int("task-timeout", envInt("PROBE_TASK_TIMEOUT", 180), "seconds before an unfinished task is failed")
	fs.IntVar(&c.RetainDays, "retain-days", envInt("PROBE_RETAIN_DAYS", 90), "delete task history older than this many days (0 = keep)")
	fs.StringVar(&c.LogLevel, "log-level", envOr("PROBE_LOG_LEVEL", "info"), "debug|info|warn|error")
	fs.StringVar(&c.BaseURL, "base-url", envOr("PROBE_BASE_URL", ""), "public dashboard URL for links in notifications, e.g. https://probe.example.com")
	monitorRetain := fs.Int("monitor-task-retain-hours", envInt("PROBE_MONITOR_TASK_RETAIN_HOURS", 48), "keep per-run details of scheduled monitors this many hours (samples are kept for retain-days)")
	fs.StringVar(&c.AgentsDir, "agents-dir", envOr("PROBE_AGENTS_DIR", ""), "directory of probe-agent-<os>-<arch> binaries to serve for agent self-update (empty = disabled)")
	fs.StringVar(&c.AgentImage, "agent-image", envOr("PROBE_AGENT_IMAGE", "ghcr.io/lochinemak/probe-agent:latest"), "agent image name shown in the dashboard's install instructions (use a registry mirror here if needed)")
	_ = fs.Parse(args)
	c.TaskTimeout = time.Duration(*timeout) * time.Second
	c.MonitorTaskRetain = time.Duration(*monitorRetain) * time.Hour
	return c
}
