// Package protocol defines the wire types shared between the probe server,
// the probe agents and the web dashboard.
package protocol

import (
	"encoding/json"
	"time"
)

// TaskType identifies a probe kind.
type TaskType string

const (
	TaskPing   TaskType = "ping"
	TaskTCPing TaskType = "tcping"
	TaskHTTP   TaskType = "http"
	TaskMTR    TaskType = "mtr"
	TaskDNS    TaskType = "dns"
	// TaskUDP sends one datagram per attempt and reports whether anything
	// came back. TaskTLS performs a handshake and reports the certificate
	// chain. TaskDNSCompare asks several resolvers the same question from one
	// node. TaskStatus reports the node's own load, memory and disk; it has
	// no target.
	TaskUDP        TaskType = "udp"
	TaskTLS        TaskType = "tls"
	TaskDNSCompare TaskType = "dns_compare"
	TaskStatus     TaskType = "status"
)

// TaskTypes lists every task type.
var TaskTypes = []TaskType{TaskPing, TaskTCPing, TaskHTTP, TaskMTR, TaskDNS, TaskUDP, TaskTLS, TaskDNSCompare, TaskStatus}

// Valid reports whether t is a known task type.
func (t TaskType) Valid() bool {
	for _, k := range TaskTypes {
		if t == k {
			return true
		}
	}
	return false
}

// Monitorable reports whether a scheduled monitor can use t: its result has
// to collapse into the monitor's latency / loss / ok sample.
func (t TaskType) Monitorable() bool {
	switch t {
	case TaskPing, TaskTCPing, TaskHTTP, TaskMTR, TaskDNS, TaskUDP, TaskTLS:
		return true
	}
	return false
}

// Capability is what an agent must report (Hello.Capabilities) to be given
// tasks of type t, "" for the types every agent has always had. A node that
// lacks it is failed by the server instead of being sent something it would
// not understand.
func (t TaskType) Capability() string {
	switch t {
	case TaskUDP:
		return "udp"
	case TaskTLS:
		return "tls"
	case TaskDNSCompare:
		return "dns_multi"
	case TaskStatus:
		return "status"
	}
	return ""
}

// CapResolver is the capability for Params.ConnectTo and Params.Resolver. An
// agent without it would silently ignore both and measure the wrong thing.
const CapResolver = "resolver"

// Message is the envelope for every WebSocket frame between agent and server.
type Message struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Message types (agent -> server).
const (
	MsgHello    = "hello"
	MsgProgress = "progress"
	MsgResult   = "result"
)

// Message types (server -> agent).
const (
	MsgWelcome = "welcome"
	MsgTask    = "task"
	MsgCancel  = "cancel"
	MsgUpdate  = "update"
)

// Update tells an agent that the server ships a different agent build for
// its platform. Path is relative to the server's HTTP root.
type Update struct {
	Version string `json:"version"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
}

// NewMessage marshals payload into a Message.
func NewMessage(typ string, payload any) (Message, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return Message{}, err
	}
	return Message{Type: typ, Payload: b}, nil
}

// Hello is the first frame an agent sends after connecting.
type Hello struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Hostname string `json:"hostname,omitempty"`
	// Location / ISP are operator-provided labels, e.g. "广东 深圳" / "电信".
	Location string   `json:"location,omitempty"`
	ISP      string   `json:"isp,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	// Capabilities lists what this agent can do: "icmp", "icmp_raw", "ipv6".
	Capabilities []string `json:"capabilities,omitempty"`
	// MaxConcurrency is how many tasks the agent will run in parallel.
	MaxConcurrency int `json:"max_concurrency,omitempty"`
	// Variant is the CPU variant for GOARCH=arm builds ("v6", "v7").
	Variant string `json:"variant,omitempty"`
	// SelfUpdate reports whether the agent acts on MsgUpdate.
	SelfUpdate bool `json:"self_update"`
	// TokenHandoff reports that the agent can store a node token handed over
	// in Welcome.Token and use it from then on (migration off the shared token).
	TokenHandoff bool `json:"token_handoff,omitempty"`
}

// Welcome is the server's reply to Hello.
type Welcome struct {
	AgentID    string `json:"agent_id"`
	PublicIP   string `json:"public_ip,omitempty"`
	ServerTime int64  `json:"server_time"`
	// Token is this node's own token. Only sent to an agent that connected
	// with the legacy shared token and set Hello.TokenHandoff; it should save
	// the token and reconnect with it.
	Token string `json:"token,omitempty"`
}

// Params carries every optional knob for every task type. Unused fields are
// simply left at zero; each probe fills in its own defaults.
type Params struct {
	// Shared
	Count      int    `json:"count,omitempty"`       // number of probes / attempts
	IntervalMs int    `json:"interval_ms,omitempty"` // gap between probes
	TimeoutMs  int    `json:"timeout_ms,omitempty"`  // per-probe timeout
	IPVersion  string `json:"ip_version,omitempty"`  // "", "4" or "6"
	// PublicOnly restricts the probe to globally routable destinations. The
	// server sets it for every task an anonymous guest starts, so untrusted
	// callers cannot aim the agents at loopback, a home LAN or cloud metadata.
	// It is never honoured from client input: the server overwrites it.
	PublicOnly bool `json:"public_only,omitempty"`
	// Continuous marks a ping / tcping run as "持续检测": a long sampling run
	// rather than a one-off check. Agents ignore it (Count and IntervalMs say
	// what to do); the server uses it for guest limits and the dashboard to
	// pick the table layout.
	Continuous bool `json:"continuous,omitempty"`

	// ping
	PacketSize int `json:"packet_size,omitempty"`

	// tcping
	Port int `json:"port,omitempty"`

	// http
	Method          string            `json:"method,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	Body            string            `json:"body,omitempty"`
	FollowRedirects bool              `json:"follow_redirects,omitempty"`
	InsecureTLS     bool              `json:"insecure_tls,omitempty"`
	ExpectStatus    int               `json:"expect_status,omitempty"`  // 0 = any status below 400
	ExpectKeyword   string            `json:"expect_keyword,omitempty"` // body must contain
	ExpectMaxMs     int               `json:"expect_max_ms,omitempty"`  // total time must be below
	SpeedTest       bool              `json:"speed_test,omitempty"`     // keep downloading to measure throughput
	SpeedSeconds    int               `json:"speed_seconds,omitempty"`  // download window, default 5

	// mtr
	MaxHops  int    `json:"max_hops,omitempty"`
	Resolve  bool   `json:"resolve,omitempty"`  // reverse-DNS hop addresses
	Protocol string `json:"protocol,omitempty"` // icmp (default), tcp, udp; Port applies to tcp/udp

	// dns
	RecordType string `json:"record_type,omitempty"` // A (default), AAAA, CNAME, MX, TXT, NS, PTR
	DNSServer  string `json:"dns_server,omitempty"`  // "" = node's system resolver; ip, ip:port, or https://.../dns-query

	// Where the probe connects (ping, tcping, http, mtr, udp, tls).
	//
	// ConnectTo makes the node probe this address or host instead of
	// resolving the target, like curl --connect-to: the target still supplies
	// the HTTP Host header, the TLS server name and the name the certificate
	// is checked against. Resolver makes the node look the target up at this
	// DNS server (same syntax as DNSServer) instead of its system resolver.
	// Both need the agent capability CapResolver.
	ConnectTo string `json:"connect_to,omitempty"`
	Resolver  string `json:"resolver,omitempty"`

	// http conveniences, applied after Headers.
	UserAgent string `json:"user_agent,omitempty"`
	Referer   string `json:"referer,omitempty"`
	Cookie    string `json:"cookie,omitempty"`

	// udp
	PayloadMode string `json:"payload_mode,omitempty"` // "" / auto (DNS query on port 53, else text), dns, text
	Payload     string `json:"payload,omitempty"`      // text mode: what to send (default "probe")
	QueryName   string `json:"query_name,omitempty"`   // dns mode: name to ask for (default example.com)
	NoWait      bool   `json:"no_wait,omitempty"`      // only test that the datagram can be sent

	// tls
	ExpectCertDays int `json:"expect_cert_days,omitempty"` // fail when the certificate expires sooner than this

	// dns_compare
	DNSServers []string `json:"dns_servers,omitempty"` // resolvers to ask; "system" is the node's own
}

// Task is a probe request created via the dashboard and dispatched to agents.
type Task struct {
	ID        string    `json:"id"`
	Type      TaskType  `json:"type"`
	Target    string    `json:"target"`
	Params    Params    `json:"params"`
	CreatedAt time.Time `json:"created_at"`
	// AgentIDs is the set of agents the task was dispatched to.
	AgentIDs []string `json:"agent_ids,omitempty"`
	// MonitorID is set when the task was created by a scheduled monitor.
	MonitorID string `json:"monitor_id,omitempty"`
	// Owner identifies who started the task: "admin:<name>" for a logged-in
	// administrator, "guest:<id>" for an anonymous visitor (the id lives in a
	// long-lived cookie), "" for scheduled monitor runs. Guests only ever see
	// tasks with their own owner; agents never receive it.
	Owner string `json:"owner,omitempty"`
}

// ---------------------------------------------------------------------------
// Scheduled monitoring
// ---------------------------------------------------------------------------

// AlertRule decides when a monitor's result for one agent counts as failing.
// A probe error, total loss, a failed HTTP assertion, an unreached MTR
// destination or a non-success DNS rcode always fail; the thresholds add to
// that. Consecutive failures are required before an alert fires.
type AlertRule struct {
	Enabled     bool    `json:"enabled"`
	LossPct     float64 `json:"loss_pct,omitempty"`    // fail when loss >= this (0 = ignore)
	LatencyMs   float64 `json:"latency_ms,omitempty"`  // fail when latency >= this (0 = ignore)
	Consecutive int     `json:"consecutive,omitempty"` // default 2
}

// Monitor is a probe that runs on a schedule.
type Monitor struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Type        TaskType   `json:"type"`
	Target      string     `json:"target"`
	Params      Params     `json:"params"`
	AgentIDs    []string   `json:"agent_ids"` // empty = every online agent
	IntervalSec int        `json:"interval_sec"`
	Enabled     bool       `json:"enabled"`
	Alert       AlertRule  `json:"alert"`
	NotifyIDs   []string   `json:"notify_ids"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	LastRunAt   *time.Time `json:"last_run_at,omitempty"`
	NextRunAt   *time.Time `json:"next_run_at,omitempty"`
}

// Sample is one monitor run summarised for one agent: a latency, a loss
// percentage and an ok flag regardless of probe type, plus a few extras.
type Sample struct {
	MonitorID      string    `json:"monitor_id"`
	AgentID        string    `json:"agent_id"`
	At             time.Time `json:"at"`
	TaskID         string    `json:"task_id,omitempty"`
	OK             bool      `json:"ok"`
	Error          string    `json:"error,omitempty"`
	LatencyMs      float64   `json:"latency_ms"` // -1 when nothing answered
	LossPct        float64   `json:"loss_pct"`
	StatusCode     int       `json:"status_code,omitempty"`
	Reached        bool      `json:"reached,omitempty"`
	ThroughputMbps float64   `json:"throughput_mbps,omitempty"`
}

// MonitorAgentState is the alerting state machine for one (monitor, agent).
type MonitorAgentState struct {
	MonitorID string     `json:"monitor_id"`
	AgentID   string     `json:"agent_id"`
	AgentName string     `json:"agent_name,omitempty"`
	Failing   int        `json:"failing"` // consecutive failures so far
	Alerting  bool       `json:"alerting"`
	Since     *time.Time `json:"since,omitempty"`
	LastError string     `json:"last_error,omitempty"`
	Last      *Sample    `json:"last,omitempty"`
}

// AlertEvent records an alert firing (down) or clearing (up).
type AlertEvent struct {
	ID        int64     `json:"id"`
	MonitorID string    `json:"monitor_id"`
	Monitor   string    `json:"monitor,omitempty"`
	AgentID   string    `json:"agent_id"`
	Agent     string    `json:"agent,omitempty"`
	Kind      string    `json:"kind"` // down, up
	Message   string    `json:"message"`
	At        time.Time `json:"at"`
}

// NotifyChannel is a configured notification destination.
type NotifyChannel struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Type    string            `json:"type"` // telegram, wecom, dingtalk, bark, webhook, smtp, pushdeer, gotify
	Config  map[string]string `json:"config"`
	Enabled bool              `json:"enabled"`
	// SecretSet tells the dashboard which credential fields have a stored
	// value. Those values are never sent back; an empty field on save means
	// "keep what is stored". Only set on responses.
	SecretSet map[string]bool `json:"secret_set,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

// SeriesPoint is one (bucketed) point of a monitor chart.
type SeriesPoint struct {
	T         int64   `json:"t"` // unix ms
	LatencyMs float64 `json:"latency_ms"`
	LossPct   float64 `json:"loss_pct"`
	OKRatio   float64 `json:"ok_ratio"` // 0..1 within the bucket
	Count     int     `json:"n"`
}

// Series is one agent's line on a monitor chart.
type Series struct {
	AgentID   string        `json:"agent_id"`
	AgentName string        `json:"agent_name"`
	Points    []SeriesPoint `json:"points"`
}

// Progress is a partial result streamed while a probe runs.
type Progress struct {
	TaskID string          `json:"task_id"`
	Seq    int             `json:"seq"`
	Kind   string          `json:"kind"` // "reply", "attempt", "hops", "resolved"
	Data   json.RawMessage `json:"data"`
	// Geo is added by the server to a "resolved" event: where the resolved
	// address is, from the offline IP database.
	Geo string `json:"geo,omitempty"`
}

// Result is the final frame for a task from one agent.
type Result struct {
	TaskID     string          `json:"task_id"`
	OK         bool            `json:"ok"`
	Error      string          `json:"error,omitempty"`
	DurationMs float64         `json:"duration_ms"`
	Data       json.RawMessage `json:"data,omitempty"`
}

// ---------------------------------------------------------------------------
// Probe result payloads
// ---------------------------------------------------------------------------

// Stats summarises a series of RTT samples.
type Stats struct {
	Sent     int     `json:"sent"`
	Received int     `json:"received"`
	LossPct  float64 `json:"loss_pct"`
	MinMs    float64 `json:"min_ms"`
	AvgMs    float64 `json:"avg_ms"`
	MaxMs    float64 `json:"max_ms"`
	StdDevMs float64 `json:"stddev_ms"`
}

// Reply is one ping / tcping / udp sample.
type Reply struct {
	Seq   int     `json:"seq"`
	OK    bool    `json:"ok"`
	RTTMs float64 `json:"rtt_ms"`
	TTL   int     `json:"ttl,omitempty"`
	Size  int     `json:"size,omitempty"`
	Error string  `json:"error,omitempty"`
	// State says what a UDP attempt saw: "reply" (a datagram came back),
	// "closed" (ICMP port unreachable), "no_reply" (nothing within the
	// timeout: filtered, or a service that does not answer this payload) or
	// "sent" (NoWait: the datagram left).
	State string `json:"state,omitempty"`
}

// PingResult is the payload for TaskPing, TaskTCPing and TaskUDP.
type PingResult struct {
	Target  string  `json:"target"`
	IP      string  `json:"ip"`
	Port    int     `json:"port,omitempty"`
	Proto   string  `json:"proto,omitempty"` // "udp" for TaskUDP
	Replies []Reply `json:"replies"`
	Stats   Stats   `json:"stats"`
}

// TLSCert is one certificate of a chain as the server presented it.
type TLSCert struct {
	Subject   string    `json:"subject"`
	Issuer    string    `json:"issuer"`
	SANs      []string  `json:"sans,omitempty"` // DNS names and IP addresses
	NotBefore time.Time `json:"not_before"`
	NotAfter  time.Time `json:"not_after"`
	Serial    string    `json:"serial,omitempty"`
	KeyType   string    `json:"key_type,omitempty"` // e.g. "ECDSA P-256", "RSA 2048"
	SigAlg    string    `json:"sig_alg,omitempty"`
	IsCA      bool      `json:"is_ca,omitempty"`
}

// TLSAttempt is one TLS handshake.
type TLSAttempt struct {
	Seq         int     `json:"seq"`
	OK          bool    `json:"ok"` // the handshake completed (whatever the certificate says)
	Error       string  `json:"error,omitempty"`
	ConnectMs   float64 `json:"connect_ms"`
	HandshakeMs float64 `json:"handshake_ms"`
}

// TLSResult is the payload for TaskTLS. The certificate fields describe the
// last successful handshake.
type TLSResult struct {
	Target     string       `json:"target"`
	ServerName string       `json:"server_name"`
	IP         string       `json:"ip"`
	Port       int          `json:"port"`
	Attempts   []TLSAttempt `json:"attempts"`
	Stats      Stats        `json:"stats"` // over HandshakeMs
	Version    string       `json:"version,omitempty"`
	Cipher     string       `json:"cipher,omitempty"`
	ALPN       string       `json:"alpn,omitempty"`
	Chain      []TLSCert    `json:"chain,omitempty"` // leaf first
	// Verified reports whether the chain validates for ServerName against the
	// node's trust store right now; VerifyError says why not.
	Verified    bool   `json:"verified"`
	VerifyError string `json:"verify_error,omitempty"`
	// DaysLeft is how long the leaf certificate is still valid, negative once
	// it has expired.
	DaysLeft    int  `json:"days_left"`
	OCSPStapled bool `json:"ocsp_stapled,omitempty"`
	// CertOK is false when ExpectCertDays was set and DaysLeft is below it.
	CertOK bool `json:"cert_ok"`
}

// DNSCompareEntry is one resolver's answer in a comparison.
type DNSCompareEntry struct {
	Server  string     `json:"server"`          // as asked for: "system", an address, a DoH URL
	Label   string     `json:"label,omitempty"` // display name when Server is a well-known resolver
	Attempt DNSAttempt `json:"attempt"`
	// Key is the sorted set of answer values, what resolvers are compared by.
	Key string `json:"key"`
}

// DNSCompareResult is the payload for TaskDNSCompare.
type DNSCompareResult struct {
	Target     string            `json:"target"`
	RecordType string            `json:"record_type"`
	Entries    []DNSCompareEntry `json:"entries"`
	// Verdict: "consistent" (every resolver that answered gave the same set),
	// "inconsistent", or "failed" (none answered).
	Verdict string `json:"verdict"`
	// Groups is how many distinct answer sets there were.
	Groups int `json:"groups"`
}

// StatusResult is the payload for TaskStatus: the node's own state.
type StatusResult struct {
	Hostname     string    `json:"hostname,omitempty"`
	OS           string    `json:"os"`
	Arch         string    `json:"arch"`
	Platform     string    `json:"platform,omitempty"` // distribution / product name and version
	Kernel       string    `json:"kernel,omitempty"`
	GoVersion    string    `json:"go_version,omitempty"`
	AgentVersion string    `json:"agent_version,omitempty"`
	UptimeSec    int64     `json:"uptime_sec,omitempty"`
	CPUs         int       `json:"cpus"`
	Load         []float64 `json:"load,omitempty"` // 1, 5, 15 minute averages; absent on Windows
	MemTotal     uint64    `json:"mem_total,omitempty"`
	MemAvailable uint64    `json:"mem_available,omitempty"`
	DiskPath     string    `json:"disk_path,omitempty"` // the filesystem the agent lives on
	DiskTotal    uint64    `json:"disk_total,omitempty"`
	DiskFree     uint64    `json:"disk_free,omitempty"`
	Capabilities []string  `json:"capabilities,omitempty"`
	Nameservers  []string  `json:"nameservers,omitempty"`
	// Running is how many tasks the agent was executing, this one included.
	Running int `json:"running,omitempty"`
}

// HTTPTiming breaks one HTTP request down by phase.
type HTTPTiming struct {
	DNSMs      float64 `json:"dns_ms"`
	ConnectMs  float64 `json:"connect_ms"`
	TLSMs      float64 `json:"tls_ms"`
	TTFBMs     float64 `json:"ttfb_ms"` // from request written to first response byte
	TransferMs float64 `json:"transfer_ms"`
	TotalMs    float64 `json:"total_ms"`
}

// Assertion is one pass/fail check applied to an HTTP response.
type Assertion struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"`
}

// HTTPAttempt is one HTTP request/response.
type HTTPAttempt struct {
	Seq           int               `json:"seq"`
	OK            bool              `json:"ok"`
	Error         string            `json:"error,omitempty"`
	URL           string            `json:"url"`
	FinalURL      string            `json:"final_url,omitempty"`
	IP            string            `json:"ip,omitempty"`
	StatusCode    int               `json:"status_code,omitempty"`
	Status        string            `json:"status,omitempty"`
	Proto         string            `json:"proto,omitempty"`
	ContentLength int64             `json:"content_length"`
	BodyBytes     int64             `json:"body_bytes"`
	Headers       map[string]string `json:"headers,omitempty"`
	Redirects     []string          `json:"redirects,omitempty"`
	TLSVersion    string            `json:"tls_version,omitempty"`
	TLSCipher     string            `json:"tls_cipher,omitempty"`
	CertSubject   string            `json:"cert_subject,omitempty"`
	CertIssuer    string            `json:"cert_issuer,omitempty"`
	CertNotAfter  *time.Time        `json:"cert_not_after,omitempty"`
	Timing        HTTPTiming        `json:"timing"`
	// FakeIP is set when the name resolved into 198.18.0.0/15 on the node
	// (Clash / OpenClash fake-IP), i.e. the request went through the
	// transparent proxy. The attempt is still a real end-to-end result for
	// "does this site work from here", but the timings describe the proxy
	// path rather than the node's own network.
	FakeIP bool `json:"fake_ip,omitempty"`
	// Assertions are the configured checks (status / keyword / max time);
	// AssertOK is false when any of them failed.
	Assertions []Assertion `json:"assertions,omitempty"`
	AssertOK   bool        `json:"assert_ok"`
	// Speed test: bytes read inside the download window and the resulting rate.
	SpeedBytes     int64   `json:"speed_bytes,omitempty"`
	SpeedMs        float64 `json:"speed_ms,omitempty"`
	ThroughputMbps float64 `json:"throughput_mbps,omitempty"`
}

// HTTPResult is the payload for TaskHTTP.
type HTTPResult struct {
	URL      string        `json:"url"`
	Attempts []HTTPAttempt `json:"attempts"`
	Stats    Stats         `json:"stats"` // over TotalMs of successful attempts
}

// MTRHop is one traceroute hop with aggregated statistics.
type MTRHop struct {
	TTL      int      `json:"ttl"`
	Hosts    []string `json:"hosts"`           // distinct responding addresses
	Names    []string `json:"names,omitempty"` // reverse DNS, same order as Hosts
	Geo      []string `json:"geo,omitempty"`   // filled by the server, same order as Hosts
	Sent     int      `json:"sent"`
	Received int      `json:"received"`
	LossPct  float64  `json:"loss_pct"`
	LastMs   float64  `json:"last_ms"`
	AvgMs    float64  `json:"avg_ms"`
	BestMs   float64  `json:"best_ms"`
	WorstMs  float64  `json:"worst_ms"`
	StdDevMs float64  `json:"stddev_ms"`
	Reached  bool     `json:"reached"` // this hop is the destination
}

// MTRResult is the payload for TaskMTR.
type MTRResult struct {
	Target   string   `json:"target"`
	IP       string   `json:"ip"`
	Protocol string   `json:"protocol"` // icmp, tcp, udp
	Port     int      `json:"port,omitempty"`
	Hops     []MTRHop `json:"hops"`
	Reached  bool     `json:"reached"`
	Rounds   int      `json:"rounds"`
}

// DNSAnswer is one resource record from a DNS reply.
type DNSAnswer struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	TTL   uint32 `json:"ttl"`
	Value string `json:"value"`
	Geo   string `json:"geo,omitempty"` // A / AAAA: where the address is; added by the server
}

// DNSAttempt is one DNS query.
type DNSAttempt struct {
	Seq       int         `json:"seq"`
	OK        bool        `json:"ok"`
	Error     string      `json:"error,omitempty"`
	Server    string      `json:"server"`
	Proto     string      `json:"proto"` // udp, tcp, doh
	RTTMs     float64     `json:"rtt_ms"`
	RCode     string      `json:"rcode,omitempty"`
	Truncated bool        `json:"truncated,omitempty"`
	Answers   []DNSAnswer `json:"answers"`
	FakeIP    bool        `json:"fake_ip,omitempty"` // an A answer fell in 198.18.0.0/15
}

// DNSResult is the payload for TaskDNS.
type DNSResult struct {
	Target     string       `json:"target"`
	RecordType string       `json:"record_type"`
	Attempts   []DNSAttempt `json:"attempts"`
	Stats      Stats        `json:"stats"`
}

// ---------------------------------------------------------------------------
// Dashboard-facing types
// ---------------------------------------------------------------------------

// Geo is where a node is and what kind of line it is on, in the fixed
// vocabulary the dashboard filters and maps by.
type Geo struct {
	CC       string `json:"cc,omitempty"`       // ISO 3166-1 alpha-2; Hong Kong, Macau and Taiwan are CN with a Province
	Country  string `json:"country,omitempty"`  // Chinese name
	Province string `json:"province,omitempty"` // short name for China ("广东", "内蒙古", "香港"); free text elsewhere
	City     string `json:"city,omitempty"`
	Line     string `json:"line,omitempty"` // 电信 | 联通 | 移动 | 广电 | 教育网 | IDC | 云厂商 | 其他
	// Src says where the values came from: "geoip" (public IP lookup),
	// "label" (the operator's location / ISP text) or "manual" (corrected by
	// an administrator).
	Src string `json:"src,omitempty"`
}

// AgentStatus is what the dashboard sees for one agent.
type AgentStatus struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Online      bool     `json:"online"`
	Location    string   `json:"location,omitempty"`
	ISP         string   `json:"isp,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	OS          string   `json:"os,omitempty"`
	Arch        string   `json:"arch,omitempty"`
	Version     string   `json:"version,omitempty"`
	PublicIP    string   `json:"public_ip,omitempty"`
	GeoLocation string   `json:"geo_location,omitempty"` // auto-detected from PublicIP
	GeoISP      string   `json:"geo_isp,omitempty"`
	// Geo is the node's place and line after weighing every source (see
	// agentGeo in the server). GeoOverride is the administrator's correction,
	// sent so the edit form can show it. GeoIP is the raw lookup result for
	// PublicIP and never leaves the server.
	Geo          *Geo     `json:"geo,omitempty"`
	GeoOverride  *Geo     `json:"geo_override,omitempty"`
	GeoIP        *Geo     `json:"-"`
	Capabilities []string `json:"capabilities,omitempty"`
	// Auth is how the node last authenticated: "token" (its own node token),
	// "legacy" (the old shared token, not migrated yet) or "" (created on the
	// dashboard, never connected).
	Auth        string     `json:"auth,omitempty"`
	FirstSeen   time.Time  `json:"first_seen"`
	LastSeen    time.Time  `json:"last_seen"`
	ConnectedAt *time.Time `json:"connected_at,omitempty"`
	Running     int        `json:"running"` // tasks currently executing
}

// ResultStatus is the lifecycle of one (task, agent) pair.
type ResultStatus string

const (
	StatusPending ResultStatus = "pending"
	StatusRunning ResultStatus = "running"
	StatusDone    ResultStatus = "done"
	StatusError   ResultStatus = "error"
)

// ErrStopped is AgentResult.Error on a StatusDone result that was cut short
// because the user stopped the task: Data holds what was measured until then.
const ErrStopped = "stopped"

// AgentResult is the dashboard view of one agent's outcome for a task.
type AgentResult struct {
	TaskID    string `json:"task_id"`
	AgentID   string `json:"agent_id"`
	AgentName string `json:"agent_name"`
	Location  string `json:"location,omitempty"`
	ISP       string `json:"isp,omitempty"`
	Geo       *Geo   `json:"geo,omitempty"` // the node's place and line when the task ran
	// TargetIP is the address this node probed (what the target resolved to
	// for it) and TargetGeo where that address is. The server fills both from
	// the result; they are not stored.
	TargetIP   string          `json:"target_ip,omitempty"`
	TargetGeo  string          `json:"target_geo,omitempty"`
	Status     ResultStatus    `json:"status"`
	Error      string          `json:"error,omitempty"`
	DurationMs float64         `json:"duration_ms,omitempty"`
	Data       json.RawMessage `json:"data,omitempty"`
	// Progress holds streamed partial events for tasks still running. It is
	// not persisted.
	Progress   []Progress `json:"progress,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// Event is what the dashboard receives over SSE.
type Event struct {
	Type    string `json:"type"` // snapshot, progress, result, done
	AgentID string `json:"agent_id,omitempty"`
	Done    bool   `json:"done,omitempty"` // set on snapshot/done when no agent is still running
	// Exactly one of the following is set depending on Type.
	Task     *Task          `json:"task,omitempty"`
	Results  []*AgentResult `json:"results,omitempty"`
	Progress *Progress      `json:"progress,omitempty"`
	Result   *AgentResult   `json:"result,omitempty"`
}
