package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"probe-platform/internal/protocol"
)

// Share links: a public, revocable URL (/s/<token>) for one task's result.
//
// Whoever holds the link sees the result without logging in, even when guest
// access is off, so what a share reveals is decided when it is created and
// enforced here, on the server: the scrubbers below rebuild every payload
// field by field and anything they do not know about is left out. A new
// result type or field is therefore hidden from shares until a scrubber is
// taught about it (TestShareRedactionCanary checks the existing ones).
//
// The four switches:
//
//	target  the probed host / URL, and everything that names it: final URL,
//	        redirects, certificate subject, response headers, DNS names
//	time    when the task ran
//	ips     the addresses the target resolved to, traceroute hops, DNS answers
//	nodes   node names and free-text locations (province and line always show)
//
// Never shared, whatever the switches: who started the task, real node ids,
// request headers and body.

type shareOpts struct{ target, time, ips, nodes bool }

func (s *Share) opts() shareOpts {
	return shareOpts{target: s.ShowTarget, time: s.ShowTime, ips: s.ShowIPs, nodes: s.ShowNodes}
}

const (
	shareModeFollow   = "follow"
	shareModeSnapshot = "snapshot"
	maxSharesPerTask  = 5
)

var shareLifetimes = map[string]time.Duration{"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour}

func newShareToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// --- scrubbing ---------------------------------------------------------------

// errorKind reduces an error message to a fixed phrase. Messages quote the
// target and the addresses involved ("dial tcp 1.2.3.4:443: …"), so a share
// that hides either gets the kind of failure and nothing else. The phrases
// are ones the dashboard's errorCategory recognises.
func errorKind(msg string) string {
	m := strings.ToLower(msg)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(m, w) {
				return true
			}
		}
		return false
	}
	switch {
	case m == "":
		return ""
	case has("fake-ip"):
		return "fake-ip"
	case m == protocol.ErrStopped:
		return protocol.ErrStopped
	case has("cancelled", "canceled"):
		return "cancelled"
	case has("agent offline", "disconnected", "send queue full"):
		return "agent offline"
	case has("unknown task type", "unsupported", "not supported"):
		return "unsupported"
	case has("not permitted", "raw icmp", "cap_net_raw", "permission denied"):
		return "not permitted"
	case has("不是公网地址"):
		return "不是公网地址"
	case has("no such host", "nxdomain", "lookup ", "server misbehaving", "rcode"):
		return "no such host"
	case has("certificate", "x509", "tls:", "handshake"):
		return "tls: handshake failure"
	case has("refused"):
		return "connection refused"
	case has("connection reset", "reset by peer", "broken pipe", "unexpected eof"):
		return "connection reset"
	case has("unreachable", "no route to host"):
		return "network is unreachable"
	case has("timeout", "timed out", "deadline exceeded"):
		if has("dial tcp", "connect:") {
			return "dial tcp: i/o timeout"
		}
		return "timeout"
	case has("100% loss"):
		return "100% loss"
	case has("destination not reached"):
		return "destination not reached"
	}
	return "failed"
}

// errText is an error message as a share may show it: verbatim only when
// neither the target nor the addresses are hidden.
func (o shareOpts) errText(msg string) string {
	if o.target && o.ips {
		return msg
	}
	return errorKind(msg)
}

func (o shareOpts) ip(s string) string {
	if o.ips {
		return s
	}
	return ""
}

func (o shareOpts) name(s string) string {
	if o.target {
		return s
	}
	return ""
}

// scrubTask is the task as a share shows it. Params are rebuilt from the
// fields that only describe how the probe ran.
func (o shareOpts) scrubTask(t *protocol.Task) *protocol.Task {
	p := t.Params
	out := &protocol.Task{
		Type:   t.Type,
		Target: o.name(t.Target),
		Params: protocol.Params{
			Count: p.Count, IntervalMs: p.IntervalMs, TimeoutMs: p.TimeoutMs, IPVersion: p.IPVersion, Continuous: p.Continuous,
			PacketSize: p.PacketSize, Method: p.Method, FollowRedirects: p.FollowRedirects, SpeedTest: p.SpeedTest, HTTP3: p.HTTP3,
			MaxHops: p.MaxHops, Resolve: p.Resolve, Protocol: p.Protocol, RecordType: p.RecordType,
			PayloadMode: p.PayloadMode, NoWait: p.NoWait, ExpectCertDays: p.ExpectCertDays,
		},
	}
	if o.target {
		out.Params.Port = p.Port
	}
	if o.ips {
		// Resolvers and the address a probe was pointed at are addresses too.
		out.Params.DNSServer, out.Params.DNSServers, out.Params.Resolver, out.Params.ConnectTo = p.DNSServer, p.DNSServers, p.Resolver, p.ConnectTo
	}
	if o.time {
		out.CreatedAt = t.CreatedAt
	}
	return out
}

// aliases gives every node of a task a stand-in id (n1, n2, …) in the task's
// own order, the same for a live stream and for the stored result.
func aliases(t *protocol.Task, results []*protocol.AgentResult) map[string]string {
	m := map[string]string{}
	for _, id := range t.AgentIDs {
		if _, ok := m[id]; !ok {
			m[id] = fmt.Sprintf("n%d", len(m)+1)
		}
	}
	for _, r := range results {
		if _, ok := m[r.AgentID]; !ok {
			m[r.AgentID] = fmt.Sprintf("n%d", len(m)+1)
		}
	}
	return m
}

func (o shareOpts) scrubResult(typ protocol.TaskType, r *protocol.AgentResult, alias string) *protocol.AgentResult {
	out := &protocol.AgentResult{
		AgentID: alias, AgentName: "节点 " + strings.TrimPrefix(alias, "n"),
		Status: r.Status, Error: o.errText(r.Error), DurationMs: r.DurationMs,
		Data: o.scrubData(typ, r.Data), TargetIP: o.ip(r.TargetIP), TargetGeo: r.TargetGeo,
	}
	if r.Error == protocol.ErrStopped {
		out.Error = r.Error
	}
	if g := r.Geo; g != nil {
		// Province and line are what the filters and maps need. The city
		// narrows a home node down further than a stranger needs to know.
		out.Geo = &protocol.Geo{CC: g.CC, Country: g.Country, Province: g.Province, Line: g.Line}
	}
	if o.nodes {
		out.AgentName, out.Location, out.ISP = r.AgentName, r.Location, r.ISP
		if r.Geo != nil {
			out.Geo.City = r.Geo.City
		}
	}
	if o.time {
		out.StartedAt, out.FinishedAt = r.StartedAt, r.FinishedAt
	}
	for _, p := range r.Progress {
		if sp, ok := o.scrubProgress(typ, p); ok {
			out.Progress = append(out.Progress, sp)
		}
	}
	return out
}

func (o shareOpts) scrubReply(r protocol.Reply) protocol.Reply {
	r.Error = o.errText(r.Error)
	return r
}

// sharedHeaders are the response headers a share may show.
var sharedHeaders = []string{"Server", "Content-Type", "Content-Encoding", "Cache-Control", "Age", "X-Cache", "Via", "Cf-Ray", "X-Powered-By", "Location"}

func (o shareOpts) scrubHTTPAttempt(a protocol.HTTPAttempt) protocol.HTTPAttempt {
	out := protocol.HTTPAttempt{
		Seq: a.Seq, OK: a.OK, Error: o.errText(a.Error), URL: o.name(a.URL), FinalURL: o.name(a.FinalURL), IP: o.ip(a.IP),
		StatusCode: a.StatusCode, Status: a.Status, Proto: a.Proto, ContentLength: a.ContentLength, BodyBytes: a.BodyBytes,
		TLSVersion: a.TLSVersion, TLSCipher: a.TLSCipher, CertIssuer: a.CertIssuer, CertNotAfter: a.CertNotAfter,
		Timing: a.Timing, FakeIP: a.FakeIP, AssertOK: a.AssertOK,
		SpeedBytes: a.SpeedBytes, SpeedMs: a.SpeedMs, ThroughputMbps: a.ThroughputMbps,
	}
	if o.target {
		out.Redirects, out.CertSubject = a.Redirects, a.CertSubject
		// Response headers can carry what the target handed this one request:
		// Set-Cookie, tokens in custom headers. Only the descriptive ones the
		// dashboard shows are passed on.
		for _, k := range sharedHeaders {
			if v, ok := a.Headers[k]; ok {
				if out.Headers == nil {
					out.Headers = map[string]string{}
				}
				out.Headers[k] = v
			}
		}
	}
	for _, as := range a.Assertions {
		if !o.target {
			as.Detail = "" // quotes the expected keyword or the body
		}
		out.Assertions = append(out.Assertions, as)
	}
	return out
}

func (o shareOpts) scrubDNSAttempt(a protocol.DNSAttempt) protocol.DNSAttempt {
	out := protocol.DNSAttempt{Seq: a.Seq, OK: a.OK, Error: o.errText(a.Error), Server: o.ip(a.Server), Proto: a.Proto, RTTMs: a.RTTMs, RCode: a.RCode, Truncated: a.Truncated, FakeIP: a.FakeIP}
	for _, ans := range a.Answers {
		s := protocol.DNSAnswer{Name: o.name(ans.Name), Type: ans.Type, TTL: ans.TTL, Geo: ans.Geo}
		switch ans.Type {
		case "A", "AAAA":
			s.Value = o.ip(ans.Value)
		default:
			// CNAME, MX, NS, TXT …: the value is a name or text that belongs
			// to the target, and often gives its hosting away too.
			if o.target && o.ips {
				s.Value = ans.Value
			}
		}
		out.Answers = append(out.Answers, s)
	}
	return out
}

func (o shareOpts) scrubTLSAttempt(a protocol.TLSAttempt) protocol.TLSAttempt {
	a.Error = o.errText(a.Error)
	return a
}

// scrubCompareEntry keeps which resolver was asked (a public resolver's
// address says nothing about the target; the node's own resolver is shown as
// "system" by name only) and hides the answers as scrubDNSAttempt does. The
// key resolvers are grouped by is replaced with a digest of itself when the
// answers are hidden, so "these three agree, that one differs" still shows.
func (o shareOpts) scrubCompareEntry(e protocol.DNSCompareEntry) protocol.DNSCompareEntry {
	out := protocol.DNSCompareEntry{Server: e.Server, Label: e.Label, Attempt: o.scrubDNSAttempt(e.Attempt), Key: e.Key}
	if !o.ips && !strings.EqualFold(e.Server, "system") {
		out.Attempt.Server = e.Attempt.Server // the public resolver that was named in the request
	}
	if !(o.target && o.ips) && e.Key != "" {
		sum := sha256.Sum256([]byte(e.Key))
		out.Key = "#" + hex.EncodeToString(sum[:4])
	}
	return out
}

func (o shareOpts) scrubHops(hops []protocol.MTRHop) []protocol.MTRHop {
	out := make([]protocol.MTRHop, len(hops))
	for i, h := range hops {
		out[i] = protocol.MTRHop{TTL: h.TTL, Geo: h.Geo, Sent: h.Sent, Received: h.Received, LossPct: h.LossPct,
			LastMs: h.LastMs, AvgMs: h.AvgMs, BestMs: h.BestMs, WorstMs: h.WorstMs, StdDevMs: h.StdDevMs, Reached: h.Reached}
		if o.ips {
			out[i].Hosts, out[i].Names = h.Hosts, h.Names
		} else {
			// Keep one empty slot per host so the location labels still line up.
			out[i].Hosts = make([]string, len(h.Hosts))
		}
	}
	return out
}

// scrubData rebuilds a final result payload. An unknown task type or a
// payload that does not parse yields nothing rather than the original.
func (o shareOpts) scrubData(typ protocol.TaskType, raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var out any
	switch typ {
	case protocol.TaskTLS:
		var d protocol.TLSResult
		if json.Unmarshal(raw, &d) != nil {
			return nil
		}
		s := protocol.TLSResult{Target: o.name(d.Target), ServerName: o.name(d.ServerName), IP: o.ip(d.IP), Stats: d.Stats,
			Version: d.Version, Cipher: d.Cipher, ALPN: d.ALPN, Verified: d.Verified, DaysLeft: d.DaysLeft, OCSPStapled: d.OCSPStapled, CertOK: d.CertOK,
			Attempts: []protocol.TLSAttempt{}}
		if o.target {
			s.Port, s.VerifyError = d.Port, d.VerifyError
		} else if d.VerifyError != "" {
			s.VerifyError = "certificate verification failed" // the real text names the host
		}
		for _, a := range d.Attempts {
			s.Attempts = append(s.Attempts, o.scrubTLSAttempt(a))
		}
		for _, c := range d.Chain {
			// Who issued a certificate and until when it is valid say little
			// about whose it is; its subject, names and serial say exactly that.
			sc := protocol.TLSCert{Issuer: c.Issuer, NotBefore: c.NotBefore, NotAfter: c.NotAfter, KeyType: c.KeyType, SigAlg: c.SigAlg, IsCA: c.IsCA}
			if o.target {
				sc.Subject, sc.Serial = c.Subject, c.Serial
				for _, san := range c.SANs {
					// A certificate may list the addresses it serves from.
					if o.ips || net.ParseIP(san) == nil {
						sc.SANs = append(sc.SANs, san)
					}
				}
			}
			if !o.target && !c.IsCA {
				sc.Issuer = "" // a private CA's name gives the organisation away
			}
			s.Chain = append(s.Chain, sc)
		}
		out = s
	case protocol.TaskDNSCompare:
		var d protocol.DNSCompareResult
		if json.Unmarshal(raw, &d) != nil {
			return nil
		}
		s := protocol.DNSCompareResult{Target: o.name(d.Target), RecordType: d.RecordType, Verdict: d.Verdict, Groups: d.Groups, Entries: []protocol.DNSCompareEntry{}}
		for _, e := range d.Entries {
			s.Entries = append(s.Entries, o.scrubCompareEntry(e))
		}
		out = s
	case protocol.TaskPing, protocol.TaskTCPing, protocol.TaskUDP:
		var d protocol.PingResult
		if json.Unmarshal(raw, &d) != nil {
			return nil
		}
		s := protocol.PingResult{Target: o.name(d.Target), IP: o.ip(d.IP), Proto: d.Proto, Stats: d.Stats, Replies: []protocol.Reply{}}
		if o.target {
			s.Port = d.Port
		}
		for _, r := range d.Replies {
			s.Replies = append(s.Replies, o.scrubReply(r))
		}
		out = s
	case protocol.TaskHTTP:
		var d protocol.HTTPResult
		if json.Unmarshal(raw, &d) != nil {
			return nil
		}
		s := protocol.HTTPResult{URL: o.name(d.URL), Stats: d.Stats, Attempts: []protocol.HTTPAttempt{}}
		for _, a := range d.Attempts {
			s.Attempts = append(s.Attempts, o.scrubHTTPAttempt(a))
		}
		out = s
	case protocol.TaskDNS:
		var d protocol.DNSResult
		if json.Unmarshal(raw, &d) != nil {
			return nil
		}
		s := protocol.DNSResult{Target: o.name(d.Target), RecordType: d.RecordType, Stats: d.Stats, Attempts: []protocol.DNSAttempt{}}
		for _, a := range d.Attempts {
			s.Attempts = append(s.Attempts, o.scrubDNSAttempt(a))
		}
		out = s
	case protocol.TaskMTR:
		var d protocol.MTRResult
		if json.Unmarshal(raw, &d) != nil {
			return nil
		}
		s := protocol.MTRResult{Target: o.name(d.Target), IP: o.ip(d.IP), Protocol: d.Protocol, Hops: o.scrubHops(d.Hops), Reached: d.Reached, Rounds: d.Rounds}
		if o.target {
			s.Port = d.Port
		}
		out = s
	default:
		return nil
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil
	}
	return b
}

// scrubProgress rebuilds one streamed event; ok is false when nothing of it
// may be shown.
func (o shareOpts) scrubProgress(typ protocol.TaskType, p protocol.Progress) (protocol.Progress, bool) {
	out := protocol.Progress{Seq: p.Seq, Kind: p.Kind}
	var v any
	switch {
	case p.Kind == "resolved":
		var ip string
		if json.Unmarshal(p.Data, &ip) != nil {
			return out, false
		}
		v, out.Geo = o.ip(ip), p.Geo
	case p.Kind == "attempt" && typ == protocol.TaskTLS:
		var a protocol.TLSAttempt
		if json.Unmarshal(p.Data, &a) != nil {
			return out, false
		}
		v = o.scrubTLSAttempt(a)
	case p.Kind == "entry" && typ == protocol.TaskDNSCompare:
		var e protocol.DNSCompareEntry
		if json.Unmarshal(p.Data, &e) != nil {
			return out, false
		}
		v = o.scrubCompareEntry(e)
	case p.Kind == "reply" && (typ == protocol.TaskPing || typ == protocol.TaskTCPing || typ == protocol.TaskUDP):
		var r protocol.Reply
		if json.Unmarshal(p.Data, &r) != nil {
			return out, false
		}
		v = o.scrubReply(r)
	case p.Kind == "attempt" && typ == protocol.TaskHTTP:
		var a protocol.HTTPAttempt
		if json.Unmarshal(p.Data, &a) != nil {
			return out, false
		}
		v = o.scrubHTTPAttempt(a)
	case p.Kind == "attempt" && typ == protocol.TaskDNS:
		var a protocol.DNSAttempt
		if json.Unmarshal(p.Data, &a) != nil {
			return out, false
		}
		v = o.scrubDNSAttempt(a)
	case p.Kind == "hops" && typ == protocol.TaskMTR:
		var hops []protocol.MTRHop
		if json.Unmarshal(p.Data, &hops) != nil {
			return out, false
		}
		v = o.scrubHops(hops)
	default:
		return out, false
	}
	b, err := json.Marshal(v)
	if err != nil {
		return out, false
	}
	out.Data = b
	return out, true
}

// scrubSnapshot is a whole task as a share shows it.
func (o shareOpts) scrubSnapshot(ev *protocol.Event) *protocol.Event {
	out := &protocol.Event{Type: "snapshot", Done: ev.Done, Task: o.scrubTask(ev.Task), Results: []*protocol.AgentResult{}}
	names := aliases(ev.Task, ev.Results)
	for _, r := range ev.Results {
		out.Results = append(out.Results, o.scrubResult(ev.Task.Type, r, names[r.AgentID]))
	}
	return out
}

// --- limits ------------------------------------------------------------------

// rateLimiter allows limit events per window for each key.
type rateLimiter struct {
	limit  int
	window time.Duration

	mu   sync.Mutex
	seen map[string][]time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{limit: limit, window: window, seen: map[string][]time.Time{}}
}

func (l *rateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.seen) > maxTrackedIPs {
		l.seen = map[string][]time.Time{} // a flood of distinct keys: start over rather than grow
	}
	recent := l.seen[key][:0]
	for _, t := range l.seen[key] {
		if now.Sub(t) < l.window {
			recent = append(recent, t)
		}
	}
	if len(recent) >= l.limit {
		l.seen[key] = recent
		return false
	}
	l.seen[key] = append(recent, now)
	return true
}

// shareLimiter bounds how fast one address can open share links, so tokens
// cannot be guessed at speed and a popular link cannot be used to hammer the
// database.
var shareLimiter = newRateLimiter(120, time.Minute)

// --- API ---------------------------------------------------------------------

// shareView is a share as its creator sees it in the list.
type shareView struct {
	*Share
	Active bool `json:"active"`
}

func viewShare(s *Share) shareView { return shareView{Share: s, Active: s.Active(time.Now())} }

// mayManageShare: administrators manage every share, anyone else only the
// ones they created.
func (a *API) mayManageShare(r *http.Request, s *Share) bool {
	if a.auth.isAdmin(r) {
		return true
	}
	owner := a.auth.owner(r)
	return owner != "" && owner == s.CreatedBy
}

func (a *API) createShare(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode       string `json:"mode"`
		Expires    string `json:"expires"`
		ShowTarget bool   `json:"show_target"`
		ShowTime   *bool  `json:"show_time"`
		ShowIPs    bool   `json:"show_ips"`
		ShowNodes  bool   `json:"show_nodes"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if body.Mode == "" {
		body.Mode = shareModeFollow
	}
	if body.Mode != shareModeFollow && body.Mode != shareModeSnapshot {
		writeErr(w, http.StatusBadRequest, "mode 必须是 follow 或 snapshot")
		return
	}
	if body.Expires == "" {
		body.Expires = "7d"
	}
	life, ok := shareLifetimes[body.Expires]
	if !ok {
		writeErr(w, http.StatusBadRequest, "有效期必须是 24h、7d 或 30d")
		return
	}
	id := r.PathValue("id")
	snap, err := a.hub.LoadTask(id)
	if errors.Is(err, ErrNotFound) || (err == nil && !a.canSeeTask(r, snap.Task)) {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if snap.Task.Type == protocol.TaskStatus {
		writeErr(w, http.StatusBadRequest, "节点状态的结果不能分享")
		return
	}
	existing, err := a.store.ListShares(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now()
	active := 0
	for _, s := range existing {
		if s.Active(now) {
			active++
		}
	}
	if active >= maxSharesPerTask {
		writeErr(w, http.StatusConflict, fmt.Sprintf("这个结果已有 %d 个有效的分享链接，请先撤销不用的", maxSharesPerTask))
		return
	}
	owner := a.auth.owner(r)
	if owner == "" {
		owner = a.auth.ensureGuestOwner(w, r)
	}
	sh := &Share{
		Token: newShareToken(), TaskID: id, Mode: body.Mode,
		ShowTarget: body.ShowTarget, ShowTime: body.ShowTime == nil || *body.ShowTime, ShowIPs: body.ShowIPs, ShowNodes: body.ShowNodes,
		CreatedBy: owner, CreatedAt: now, ExpiresAt: now.Add(life),
	}
	// A snapshot is frozen as it stands; so is a share of a task that has
	// already finished. Only "follow" on a running task stays open.
	if sh.Mode == shareModeSnapshot || snap.Done {
		frozen := sh.opts().scrubSnapshot(snap)
		frozen.Done = true
		if sh.Snapshot, err = json.Marshal(frozen); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := a.store.InsertShare(sh); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.log.Info("share created", "task", id, "mode", sh.Mode, "expires", body.Expires, "by", owner)
	writeJSON(w, http.StatusCreated, map[string]any{"share": viewShare(sh)})
}

func (a *API) listShares(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if snap, err := a.hub.LoadTask(id); err != nil || !a.canSeeTask(r, snap.Task) {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	all, err := a.store.ListShares(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []shareView{}
	for _, s := range all {
		if a.mayManageShare(r, s) {
			out = append(out, viewShare(s))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"shares": out})
}

func (a *API) revokeShare(w http.ResponseWriter, r *http.Request) {
	s, err := a.store.GetShare(r.PathValue("token"))
	if err != nil || !a.mayManageShare(r, s) {
		writeErr(w, http.StatusNotFound, "分享不存在")
		return
	}
	if err := a.store.RevokeShare(s.Token, time.Now()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// freezeShares is a task-done hook: every "follow" share of the task gets the
// finished result stored with it, so it keeps opening after the task itself
// has been pruned.
func (a *API) freezeShares(task protocol.Task, results []*protocol.AgentResult) {
	shares, err := a.store.ListShares(task.ID)
	if err != nil || len(shares) == 0 {
		return
	}
	now := time.Now()
	for _, s := range shares {
		if len(s.Snapshot) > 0 || !s.Active(now) {
			continue
		}
		frozen := s.opts().scrubSnapshot(&protocol.Event{Task: &task, Results: results, Done: true})
		b, err := json.Marshal(frozen)
		if err == nil {
			err = a.store.FreezeShare(s.Token, b)
		}
		if err != nil {
			a.log.Error("freeze share", "task", task.ID, "err", err)
		}
	}
}

// publicShare is what an open share link says about itself.
type publicShare struct {
	Mode       string     `json:"mode"`
	ShowTarget bool       `json:"show_target"`
	ShowTime   bool       `json:"show_time"`
	ShowIPs    bool       `json:"show_ips"`
	ShowNodes  bool       `json:"show_nodes"`
	ExpiresAt  time.Time  `json:"expires_at"`
	CreatedAt  *time.Time `json:"created_at,omitempty"`
	// Live is true while a "follow" share's task is still running: the page
	// should open the event stream.
	Live bool `json:"live"`
}

const shareGone = "分享链接无效、已过期或已被撤销"

// openShare resolves a token for a public request: the share and its result,
// already scrubbed. live is true when the task is still running.
func (a *API) openShare(w http.ResponseWriter, r *http.Request) (s *Share, snap *protocol.Event, live bool) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	if !shareLimiter.allow(a.cfg.ClientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, "请求过于频繁，请稍后再试")
		return nil, nil, false
	}
	s, err := a.store.GetShare(r.PathValue("token"))
	if err != nil || !s.Active(time.Now()) {
		writeErr(w, http.StatusNotFound, shareGone)
		return nil, nil, false
	}
	if len(s.Snapshot) > 0 {
		var frozen protocol.Event
		if err := json.Unmarshal(s.Snapshot, &frozen); err != nil {
			writeErr(w, http.StatusInternalServerError, "分享数据损坏")
			return nil, nil, false
		}
		return s, &frozen, false
	}
	// A "follow" share whose task has not finished (or finished without the
	// hook having run yet).
	cur, err := a.hub.LoadTask(s.TaskID)
	if err != nil {
		writeErr(w, http.StatusNotFound, shareGone)
		return nil, nil, false
	}
	return s, s.opts().scrubSnapshot(cur), !cur.Done
}

func (a *API) getShare(w http.ResponseWriter, r *http.Request) {
	s, snap, live := a.openShare(w, r)
	if s == nil {
		return
	}
	if err := a.store.AddShareVisit(s.Token); err != nil {
		a.log.Error("share visit", "err", err)
	}
	pub := publicShare{Mode: s.Mode, ShowTarget: s.ShowTarget, ShowTime: s.ShowTime, ShowIPs: s.ShowIPs, ShowNodes: s.ShowNodes, ExpiresAt: s.ExpiresAt, Live: live}
	if s.ShowTime {
		pub.CreatedAt = &s.CreatedAt
	}
	writeJSON(w, http.StatusOK, map[string]any{"share": pub, "task": snap.Task, "results": snap.Results, "done": snap.Done || !live})
}

// shareEvents streams a "follow" share's task while it runs, scrubbing every
// event on the way out. For a frozen share it sends the result and ends.
func (a *API) shareEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	s, snap, live := a.openShare(w, r)
	if s == nil {
		return
	}
	o := s.opts()
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
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")

	var events <-chan protocol.Event
	var names map[string]string
	var typ protocol.TaskType
	if live {
		cur, ch, unsub, ok := a.hub.Subscribe(s.TaskID)
		if ok {
			defer unsub()
			// Scrub the snapshot taken together with the subscription, so
			// nothing falls between it and the first event.
			snap, events, names, typ = o.scrubSnapshot(&cur), ch, aliases(cur.Task, cur.Results), cur.Task.Type
			live = !cur.Done
		} else {
			live = false
		}
	}
	w.WriteHeader(http.StatusOK)
	snap.Type = "snapshot"
	snap.Done = snap.Done || !live
	if err := send(*snap); err != nil {
		return
	}
	if !live {
		_ = send(protocol.Event{Type: "done", Done: true})
		return
	}
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()
	expired := time.NewTimer(time.Until(s.ExpiresAt))
	defer expired.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-expired.C:
			return
		case ev := <-events:
			alias := names[ev.AgentID]
			switch ev.Type {
			case "progress":
				if ev.Progress == nil || alias == "" {
					continue
				}
				p, ok := o.scrubProgress(typ, *ev.Progress)
				if !ok {
					continue
				}
				if err := send(protocol.Event{Type: "progress", AgentID: alias, Progress: &p}); err != nil {
					return
				}
			case "result":
				if ev.Result == nil || alias == "" {
					continue
				}
				if err := send(protocol.Event{Type: "result", AgentID: alias, Result: o.scrubResult(typ, ev.Result, alias)}); err != nil {
					return
				}
			case "done":
				_ = send(protocol.Event{Type: "done", Done: true})
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
