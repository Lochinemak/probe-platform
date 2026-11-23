package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"probe-platform/internal/probe"
	"probe-platform/internal/protocol"
)

// Global DNS propagation check: after a record is changed, where has the new
// value arrived? Four steps, streamed to the browser as they finish:
//
//	expect      what the user expects the record to be (optional)
//	delegation  walk from the root to the zone that holds the name
//	authority   ask every name server of that zone directly (RD=0)
//	recursive   ask the public resolvers of the directory (RD=1)
//
// The queries leave from the server itself, so the picture is the one seen
// from the server's network. An administrator can hand the last step to a
// node instead (it runs there as ordinary dns_compare tasks).
//
// Nothing here is stored. What keeps it from being a query cannon: every
// exchange goes to port 53 of a public address only (a zone can point its NS
// records anywhere), at most propMaxQueries exchanges and propDeadline per
// run, propSlots runs at a time, a few runs a minute per visitor, and a short
// cache so reloading the page does not run it again. Questions leave the
// server no closer together than propSpacing.
const (
	propDeadline    = 20 * time.Second
	propMaxQueries  = 150
	propConcurrency = 40
	propMaxNS       = 10
	propSlots       = 2
	propCacheTTL    = 30 * time.Second
	propHopTimeout  = 1500 * time.Millisecond
	propAskTimeout  = 2 * time.Second
)

// propTypes are the record types the check accepts, in the order the form
// lists them.
var propTypes = []string{"A", "AAAA", "CNAME", "MX", "TXT", "NS", "SOA", "CAA", "SRV", "PTR", "DS", "DNSKEY", "TLSA", "SVCB", "HTTPS"}

type nsAddr struct{ Host, Addr string }

// rootHints are the IPv4 addresses of the thirteen root servers.
var rootHints = []nsAddr{
	{"a.root-servers.net", "198.41.0.4"}, {"b.root-servers.net", "170.247.170.2"}, {"c.root-servers.net", "192.33.4.12"},
	{"d.root-servers.net", "199.7.91.13"}, {"e.root-servers.net", "192.203.230.10"}, {"f.root-servers.net", "192.5.5.241"},
	{"g.root-servers.net", "192.112.36.4"}, {"h.root-servers.net", "198.97.190.53"}, {"i.root-servers.net", "192.36.148.17"},
	{"j.root-servers.net", "192.58.128.30"}, {"k.root-servers.net", "193.0.14.129"}, {"l.root-servers.net", "199.7.83.42"},
	{"m.root-servers.net", "202.12.27.33"},
}

type dnsExchangeFunc func(ctx context.Context, q probe.DNSQuestion) (*probe.DNSMessage, error)

// What a new checker starts from; tests put a fake DNS in their place.
var (
	propExchange   dnsExchangeFunc = probe.DNSExchange
	propRoots                      = rootHints
	propResolvers                  = func() []Resolver { return resolvers }
	propLookupHost                 = func(ctx context.Context, host string) ([]string, error) {
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
		out := make([]string, 0, len(ips))
		for _, ip := range ips {
			out = append(out, ip.String())
		}
		return out, err
	}
)

// propChecker holds what a run needs and what runs share. The function
// fields are the test seams.
type propChecker struct {
	exchange   dnsExchangeFunc
	lookupHost func(ctx context.Context, host string) ([]string, error) // IPv4 addresses of a name server without glue
	roots      []nsAddr
	resolvers  []Resolver
	publicOnly bool
	geo        *GeoIP

	slots   chan struct{}
	limiter *rateLimiter
	mu      sync.Mutex
	cache   map[string]propCached
}

type propCached struct {
	at     time.Time
	events []propEvent
}

func newPropChecker(geo *GeoIP) *propChecker {
	return &propChecker{
		exchange:   propExchange,
		lookupHost: propLookupHost,
		roots:      propRoots,
		resolvers:  propResolvers(),
		publicOnly: true,
		geo:        geo,
		slots:      make(chan struct{}, propSlots),
		limiter:    newRateLimiter(3, time.Minute),
		cache:      map[string]propCached{},
	}
}

// propRequest is one validated check.
type propRequest struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Tier   string `json:"tier"`             // quick | full
	Expect string `json:"expect,omitempty"` // as typed
	Agent  string `json:"agent,omitempty"`  // node that runs the recursive step; "" = the server
	owner  string
}

func (q propRequest) key() string {
	return strings.Join([]string{q.Name, q.Type, q.Tier, q.Expect, q.Agent}, "\x00")
}

var propLabelRe = regexp.MustCompile(`^[a-z0-9_]([a-z0-9_-]*[a-z0-9_])?$`)

// parsePropRequest validates the query string. Errors are shown to the user.
func parsePropRequest(name, typ, tier, expect string) (propRequest, error) {
	req := propRequest{Type: strings.ToUpper(strings.TrimSpace(typ)), Tier: tier, Expect: strings.TrimSpace(expect)}
	if req.Type == "" {
		req.Type = "A"
	}
	known := false
	for _, t := range propTypes {
		known = known || t == req.Type
	}
	if !known {
		return req, errors.New("不支持的记录类型")
	}
	if req.Tier != "full" {
		req.Tier = "quick"
	}
	if len(req.Expect) > 512 {
		return req, errors.New("期望值过长")
	}
	name = normName(name)
	if ip := net.ParseIP(name); ip != nil {
		// An address stands for its reverse name, which is what PTR is asked of.
		if req.Type != "PTR" {
			return req, errors.New("这里填域名；IP 地址只能配合 PTR 记录查反向解析")
		}
		name = reverseName(ip)
	}
	if name == "" || len(name) > 253 {
		return req, errors.New("请输入域名")
	}
	for _, l := range strings.Split(name, ".") {
		if len(l) > 63 || !propLabelRe.MatchString(l) {
			return req, errors.New("域名格式不正确（中文域名请用 xn-- 形式）")
		}
	}
	req.Name = name
	return req, nil
}

// reverseName is the in-addr.arpa / ip6.arpa name of an address.
func reverseName(ip net.IP) string {
	if v4 := ip.To4(); v4 != nil {
		return fmt.Sprintf("%d.%d.%d.%d.in-addr.arpa", v4[3], v4[2], v4[1], v4[0])
	}
	const hex = "0123456789abcdef"
	var b strings.Builder
	for i := len(ip) - 1; i >= 0; i-- {
		b.WriteByte(hex[ip[i]&0xf])
		b.WriteByte('.')
		b.WriteByte(hex[ip[i]>>4])
		b.WriteByte('.')
	}
	return b.String() + "ip6.arpa"
}

// --- what the browser receives ---

// propEvent is one SSE event; Type is also the event name.
type propEvent struct {
	Type    string       `json:"type"` // start | stage | row | done
	Request *propRequest `json:"request,omitempty"`
	Stage   *propStage   `json:"stage,omitempty"`
	Row     *propRow     `json:"row,omitempty"`
	Summary *propSummary `json:"summary,omitempty"`
}

type propStage struct {
	ID    string `json:"id"`    // expect | delegation | authority | recursive
	State string `json:"state"` // running | done | failed | skipped
	Note  string `json:"note,omitempty"`
	Total int    `json:"total,omitempty"` // rows this stage will send

	// delegation
	Hops []propHop `json:"hops,omitempty"`
	Zone string    `json:"zone,omitempty"` // the zone that holds the name ("." for the root)
	// authority
	NSParent []string `json:"ns_parent,omitempty"` // name servers the parent zone delegates to
	NSChild  []string `json:"ns_child,omitempty"`  // name servers the zone lists itself
	// expect
	Expect []string `json:"expect,omitempty"`
}

// propHop is one question asked on the way down from the root.
type propHop struct {
	Zone   string   `json:"zone"` // zone of the server asked
	Server string   `json:"server"`
	Addr   string   `json:"addr"`
	RTTMs  float64  `json:"rtt_ms"`
	Result string   `json:"result"`         // referral | answer | nxdomain | odd | error
	Next   string   `json:"next,omitempty"` // zone it referred to
	NS     []string `json:"ns,omitempty"`
	Error  string   `json:"error,omitempty"`
}

// propRow is one server's answer in the authority or recursive stage.
type propRow struct {
	Stage   string               `json:"stage"`
	ID      string               `json:"id"`
	Name    string               `json:"name"` // NS host name, or the resolver's name
	Org     string               `json:"org,omitempty"`
	CC      string               `json:"cc,omitempty"`
	Addr    string               `json:"addr"`
	OK      bool                 `json:"ok"` // a DNS reply came back, whatever it says
	RCode   string               `json:"rcode,omitempty"`
	AA      bool                 `json:"aa,omitempty"`
	RTTMs   float64              `json:"rtt_ms"`
	Answers []protocol.DNSAnswer `json:"answers"`
	Values  []string             `json:"values"`           // the asked type's values, normalised and sorted
	CNAME   string               `json:"cname,omitempty"`  // the name is an alias of this
	Serial  uint32               `json:"serial,omitempty"` // authority: the zone's SOA serial on this server
	Error   string               `json:"error,omitempty"`
	// Verdict against the reference: match | differ | empty | unknown, or ""
	// while there is nothing to compare with yet.
	Verdict string `json:"verdict,omitempty"`
}

type propGroup struct {
	Values []string `json:"values"`
	Count  int      `json:"count"`
}

// propSummary closes a run. The percentages follow the page's wording:
// confirm = resolvers already giving the reference value, of all asked;
// comparable = the same, of those that gave an answer that can be compared;
// coverage = resolvers that replied at all.
type propSummary struct {
	Reference     []string          `json:"reference"`
	RefSource     string            `json:"ref_source"` // expect | authority | majority | ""
	Total         int               `json:"total"`
	Responded     int               `json:"responded"`
	Match         int               `json:"match"`
	Differ        int               `json:"differ"`
	Empty         int               `json:"empty"`
	Unknown       int               `json:"unknown"`
	ConfirmPct    float64           `json:"confirm_pct"`
	ComparablePct float64           `json:"comparable_pct"`
	CoveragePct   float64           `json:"coverage_pct"`
	Groups        []propGroup       `json:"groups"`
	Verdicts      map[string]string `json:"verdicts"` // row id → final verdict
	Queries       int               `json:"queries"`
	ElapsedMs     int64             `json:"elapsed_ms"`
	Vantage       string            `json:"vantage"`
	Notes         []string          `json:"notes,omitempty"`
}

// --- one run ---

type propRun struct {
	c       *propChecker
	req     propRequest
	hub     *Hub
	emit    func(propEvent)
	queries atomic.Int32
	notes   []string
	// nsListed is how many name servers the last referral named; only the
	// first propMaxNS are asked.
	nsListed int
	// nsNames are all of them, for comparing with the zone's own NS records.
	nsNames []string
}

var errPropBudget = errors.New("本次检测的查询次数已用完")

// propSpacing is the least time between two questions leaving the server,
// across all runs. Sent all at once, a few dozen datagrams from the server's
// container mostly went unanswered (even by resolvers a few milliseconds
// away), while fourteen at once always got through; spaced out they do.
var propSpacing = 40 * time.Millisecond

var propPacer struct {
	mu   sync.Mutex
	next time.Time
}

// pace waits for this question's turn. It returns false when ctx ends first.
func pace(ctx context.Context) bool {
	if propSpacing <= 0 {
		return ctx.Err() == nil
	}
	propPacer.mu.Lock()
	at := time.Now()
	if propPacer.next.After(at) {
		at = propPacer.next
	}
	propPacer.next = at.Add(propSpacing)
	propPacer.mu.Unlock()
	t := time.NewTimer(time.Until(at))
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// ask sends one question to port 53 of addr.
func (r *propRun) ask(ctx context.Context, addr, name, typ string, recurse bool, timeout time.Duration) (*probe.DNSMessage, error) {
	if r.c.publicOnly && !probe.IsPublicIP(net.ParseIP(addr)) {
		return nil, errors.New("不是公网地址，已拒绝查询")
	}
	if r.queries.Add(1) > propMaxQueries {
		return nil, errPropBudget
	}
	if !pace(ctx) {
		return nil, errors.New("cancelled")
	}
	return r.c.exchange(ctx, probe.DNSQuestion{Name: name, Type: typ, Recurse: recurse, Server: net.JoinHostPort(addr, "53"), Timeout: timeout, PublicOnly: r.c.publicOnly})
}

// askTwice is ask with one more try when the first gets no reply: one lost
// datagram should not read as a server that is down or cannot be judged.
func (r *propRun) askTwice(ctx context.Context, addr, name, typ string, recurse bool, timeout time.Duration) (*probe.DNSMessage, error) {
	m, err := r.ask(ctx, addr, name, typ, recurse, timeout)
	if err != nil && ctx.Err() == nil && !errors.Is(err, errPropBudget) && r.c.publicOnlyOK(addr) {
		m, err = r.ask(ctx, addr, name, typ, recurse, timeout)
	}
	return m, err
}

// publicOnlyOK reports whether addr may be asked at all; a refused address
// is not worth a second try.
func (c *propChecker) publicOnlyOK(addr string) bool {
	return !c.publicOnly || probe.IsPublicIP(net.ParseIP(addr))
}

func normName(s string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".")
}

func labelCount(n string) int {
	if n == "" {
		return 0
	}
	return strings.Count(n, ".") + 1
}

// inZone reports whether name is zone or below it; "" is the root.
func inZone(name, zone string) bool {
	return zone == "" || name == zone || strings.HasSuffix(name, "."+zone)
}

func zoneLabel(z string) string {
	if z == "" {
		return "."
	}
	return z + "."
}

// referralOf reads a referral out of a reply: the deepest zone below `zone`
// that contains qname and has NS records in the authority section.
func referralOf(m *probe.DNSMessage, qname, zone string) (next string, ns []string, found bool) {
	for _, rr := range m.Authority {
		owner := normName(rr.Name)
		if rr.Type != "NS" || !inZone(qname, owner) || labelCount(owner) <= labelCount(zone) {
			continue
		}
		if !found || labelCount(owner) > labelCount(next) {
			next, ns, found = owner, nil, true
		}
		if owner == next {
			ns = append(ns, normName(rr.Value))
		}
	}
	sort.Strings(ns)
	return next, ns, found
}

// addresses turns a referral's name servers into addresses: glue first, the
// system resolver for those without.
func (r *propRun) addresses(ctx context.Context, m *probe.DNSMessage, ns []string) []nsAddr {
	r.nsListed = len(ns)
	if len(ns) > propMaxNS {
		ns = ns[:propMaxNS]
	}
	glue := map[string]string{}
	for _, rr := range m.Additional {
		if rr.Type == "A" && glue[normName(rr.Name)] == "" {
			glue[normName(rr.Name)] = rr.Value
		}
	}
	out := make([]nsAddr, len(ns))
	var wg sync.WaitGroup
	for i, host := range ns {
		out[i] = nsAddr{Host: host, Addr: glue[host]}
		if out[i].Addr != "" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			lctx, cancel := context.WithTimeout(ctx, propHopTimeout)
			defer cancel()
			if ips, err := r.c.lookupHost(lctx, host); err == nil && len(ips) > 0 {
				out[i].Addr = ips[0]
			}
		}()
	}
	wg.Wait()
	return out
}

func (r *propRun) note(s string) {
	for _, n := range r.notes {
		if n == s {
			return
		}
	}
	r.notes = append(r.notes, s)
}

// walk follows referrals from the root until a server answers for the name
// itself. It returns the zone that holds the name and that zone's servers.
//
// At each level up to three servers are asked at once and the first usable
// reply wins, with a second batch if none of the first gives one: from some
// networks a root or TLD server is out of reach now and then, and waiting
// for them one after another would use up the run's time.
func (r *propRun) walk(ctx context.Context) (zone string, servers []nsAddr, hops []propHop, nx bool, err error) {
	servers = append([]nsAddr(nil), r.c.roots...)
	rand.Shuffle(len(servers), func(i, j int) { servers[i], servers[j] = servers[j], servers[i] })
	for depth := 0; depth < 12; depth++ {
		var usable []nsAddr
		for _, s := range servers {
			if s.Addr != "" {
				usable = append(usable, s)
			}
		}
		if len(usable) == 0 {
			return zone, servers, hops, false, errors.New(zoneLabel(zone) + " 的权威服务器都没有可用的 IPv4 地址")
		}
		var won *hopReply
		for start := 0; start < len(usable) && start < 2*propRace && won == nil; start += propRace {
			var failed []propHop
			won, failed = r.raceHop(ctx, zone, usable[start:min(start+propRace, len(usable))])
			hops = append(hops, failed...)
			if won != nil {
				hops = append(hops, won.hop)
			}
			r.emitHops(hops)
			if ctx.Err() != nil || r.queries.Load() > propMaxQueries {
				break
			}
		}
		if won == nil {
			return zone, servers, hops, false, errors.New(zoneLabel(zone) + " 的服务器没有给出可用的应答")
		}
		if won.hop.Result != "referral" {
			return r.apex(ctx, won.hop.Addr, zone), servers, hops, won.hop.Result == "nxdomain", nil
		}
		zone, r.nsNames = won.next, won.ns
		servers = r.addresses(ctx, won.msg, won.ns)
	}
	return zone, servers, hops, false, errors.New("委派层级过深")
}

// propRace is how many servers of a zone are asked at once on the way down.
const propRace = 3

type hopReply struct {
	hop  propHop
	msg  *probe.DNSMessage
	next string
	ns   []string
}

// raceHop asks the servers at once and returns the first reply that is an
// authoritative answer or a referral, plus the attempts that failed before it.
func (r *propRun) raceHop(ctx context.Context, zone string, servers []nsAddr) (*hopReply, []propHop) {
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch := make(chan hopReply, len(servers))
	for _, s := range servers {
		go func() {
			rep := hopReply{hop: propHop{Zone: zoneLabel(zone), Server: s.Host, Addr: s.Addr}}
			m, err := r.ask(cctx, s.Addr, r.req.Name, r.req.Type, false, propHopTimeout)
			if err != nil {
				rep.hop.Result, rep.hop.Error = "error", err.Error()
				ch <- rep
				return
			}
			rep.msg, rep.hop.RTTMs = m, m.RTTMs
			next, ns, isRef := referralOf(m, r.req.Name, zone)
			switch {
			case m.Authoritative && !isRef:
				rep.hop.Result = "answer"
				if m.RCode == "NameError" {
					rep.hop.Result = "nxdomain"
				}
			case isRef:
				rep.hop.Result, rep.hop.Next, rep.hop.NS = "referral", zoneLabel(next), ns
				rep.next, rep.ns = next, ns
			default:
				// Neither an answer the server vouches for nor a pointer
				// further down: not something a root or TLD server says.
				// Seen when a reply is forged on the way.
				rep.hop.Result, rep.hop.Error = "odd", "应答既不是权威应答也不是转介（rcode "+m.RCode+"）"
			}
			ch <- rep
		}()
	}
	var failed []propHop
	for range servers {
		rep := <-ch
		if rep.hop.Result == "answer" || rep.hop.Result == "nxdomain" || rep.hop.Result == "referral" {
			return &rep, failed
		}
		failed = append(failed, rep.hop)
	}
	return nil, failed
}

// apex narrows the zone down when one set of servers holds a zone and its
// child (they answer for the child without referring to it): the SOA that
// comes back for the name, in the answer or with an empty answer, is owned
// by the zone the name is really in.
func (r *propRun) apex(ctx context.Context, addr, zone string) string {
	m, err := r.ask(ctx, addr, r.req.Name, "SOA", false, propHopTimeout)
	if err != nil || !m.Authoritative {
		return zone
	}
	for _, rr := range append(append([]protocol.DNSAnswer(nil), m.Answers...), m.Authority...) {
		if owner := normName(rr.Name); rr.Type == "SOA" && inZone(r.req.Name, owner) && labelCount(owner) > labelCount(zone) {
			return owner
		}
	}
	return zone
}

func (r *propRun) emitHops(hops []propHop) {
	r.emit(propEvent{Type: "stage", Stage: &propStage{ID: "delegation", State: "running", Hops: append([]propHop(nil), hops...)}})
}

// nameTypes hold host names, compared case-insensitively.
var nameTypes = map[string]bool{"CNAME": true, "NS": true, "MX": true, "PTR": true, "SRV": true, "SOA": true, "SVCB": true, "HTTPS": true}

func normValue(typ, v string) string {
	v = strings.TrimSpace(v)
	switch {
	case typ == "A" || typ == "AAAA":
		if ip := net.ParseIP(v); ip != nil {
			return ip.String()
		}
	case typ == "TXT":
		return strings.Trim(v, `"`)
	case nameTypes[typ]:
		return strings.TrimSuffix(strings.ToLower(v), ".")
	}
	return v
}

// propValues is the sorted set of the asked type's values in an answer
// section, and the alias target when the name is a CNAME.
func propValues(answers []protocol.DNSAnswer, typ string) (values []string, cname string) {
	seen := map[string]bool{}
	for _, a := range answers {
		if a.Type == "CNAME" && cname == "" {
			cname = normValue("CNAME", a.Value)
		}
		if a.Type != typ {
			continue
		}
		v := normValue(typ, a.Value)
		if typ == "SOA" {
			// Servers of one zone agree on the serial or they do not; the
			// rest of the record does not change with a zone edit.
			if i := strings.LastIndex(v, "serial="); i >= 0 {
				v = v[i:]
			}
		}
		if !seen[v] {
			seen[v] = true
			values = append(values, v)
		}
	}
	sort.Strings(values)
	if typ == "CNAME" {
		cname = ""
	}
	return values, cname
}

func (r *propRun) row(stage, id, name, addr string, m *probe.DNSMessage, err error) *propRow {
	row := &propRow{Stage: stage, ID: id, Name: name, Addr: addr, Answers: []protocol.DNSAnswer{}, Values: []string{}}
	if err != nil {
		row.Error = err.Error()
		return row
	}
	row.OK, row.RCode, row.AA, row.RTTMs = true, m.RCode, m.Authoritative, m.RTTMs
	if len(m.Answers) > 0 {
		row.Answers = m.Answers
		r.c.geo.AnnotateDNS([]protocol.DNSAttempt{{Answers: row.Answers}})
	}
	if v, cname := propValues(m.Answers, r.req.Type); true {
		row.CNAME = cname
		if v != nil {
			row.Values = v
		}
	}
	return row
}

// expectValues splits what the user typed into the values to look for.
func expectValues(typ, s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := []string{s}
	if typ != "TXT" {
		parts = strings.FieldsFunc(s, func(c rune) bool { return c == ',' || c == '，' || c == '\n' || c == ';' })
	}
	var out []string
	for _, p := range parts {
		if v := normValue(typ, p); v != "" {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// verdictOf compares one row with the reference.
func verdictOf(row *propRow, typ string, ref []string, source string) string {
	if !row.OK || (row.RCode != "Success" && row.RCode != "NameError") {
		return "unknown"
	}
	if len(row.Values) == 0 {
		// An alias on the zone's own server: the record is somewhere else,
		// which is not the same as there being none.
		if row.Stage == "authority" && row.CNAME != "" {
			return ""
		}
		return "empty"
	}
	if source == "" {
		return ""
	}
	if source == "expect" {
		for _, want := range ref {
			found := false
			for _, v := range row.Values {
				// A TXT expectation may be a part of the record ("v=spf1").
				found = found || v == want || (typ == "TXT" && strings.Contains(v, want))
			}
			if !found {
				return "differ"
			}
		}
		return "match"
	}
	if strings.Join(row.Values, "\x00") == strings.Join(ref, "\x00") {
		return "match"
	}
	return "differ"
}

type propTarget struct {
	res  Resolver
	addr string
}

func (c *propChecker) targets(tier string) []propTarget {
	var out []propTarget
	for _, res := range c.resolvers {
		if res.Tier != "quick" && !(tier == "full" && res.Tier == "full") {
			continue
		}
		for _, ip := range res.IPv4 {
			out = append(out, propTarget{res, ip})
		}
	}
	return out
}

// run performs the whole check, sending events as it goes. It returns false
// when it was cut short by the caller going away.
func (c *propChecker) run(ctx context.Context, req propRequest, hub *Hub, emit func(propEvent)) bool {
	started := time.Now()
	r := &propRun{c: c, req: req, hub: hub, emit: emit}
	emit(propEvent{Type: "start", Request: &req})

	// 1. What the user expects.
	expect := expectValues(req.Type, req.Expect)
	if len(expect) > 0 {
		emit(propEvent{Type: "stage", Stage: &propStage{ID: "expect", State: "done", Expect: expect, Note: "以你填的期望值为准，逐台比对"}})
	} else {
		emit(propEvent{Type: "stage", Stage: &propStage{ID: "expect", State: "skipped", Note: "没有填期望值：以权威服务器的应答为准；权威不一致或拿不到时，以多数解析器的结果为准"}})
	}

	// 2. Delegation.
	emit(propEvent{Type: "stage", Stage: &propStage{ID: "delegation", State: "running"}})
	zone, servers, hops, nx, werr := r.walk(ctx)
	st := &propStage{ID: "delegation", State: "done", Hops: hops, Zone: zoneLabel(zone)}
	switch {
	case werr != nil:
		st.State, st.Note = "failed", werr.Error()
	case nx:
		st.Note = zoneLabel(zone) + " 的权威服务器回答：这个域名不存在（NXDOMAIN）"
	default:
		st.Note = "域名由 " + zoneLabel(zone) + " 区负责"
	}
	emit(propEvent{Type: "stage", Stage: st})

	// 3. The zone's own servers.
	var authRows []*propRow
	if werr != nil {
		emit(propEvent{Type: "stage", Stage: &propStage{ID: "authority", State: "skipped", Note: "没有找到负责这个域名的区，跳过"}})
	} else if nx && labelCount(zone) <= 1 {
		// The registry says there is no such domain: it has no servers of
		// its own to ask, and the registry's dozen say the same thing.
		emit(propEvent{Type: "stage", Stage: &propStage{ID: "authority", State: "skipped", Note: "域名没有注册或没有委派，没有它自己的权威服务器可问"}})
	} else {
		authRows = r.authority(ctx, zone, servers)
	}

	if werr == nil && r.nsListed > len(servers) && len(hops) > 0 && hops[len(hops)-1].Result != "referral" && zone != "" {
		r.note(fmt.Sprintf("%s 有 %d 台权威服务器，只检查了前 %d 台", zoneLabel(zone), r.nsListed, len(servers)))
	}
	// An alias: the record itself lives in the target's zone, which this
	// zone's servers do not answer for.
	for _, row := range authRows {
		if row.OK && row.CNAME != "" && len(row.Values) == 0 {
			r.note("这个名字是 CNAME（指向 " + row.CNAME + "），最终记录由对方所在的区给出，所以权威这一步没有可比对的值")
			break
		}
	}

	// The reference the resolvers are measured against.
	ref, source := expect, ""
	if len(expect) > 0 {
		source = "expect"
	} else if v, ok := agreedValues(authRows); ok {
		ref, source = v, "authority"
	}
	for _, row := range authRows {
		row.Verdict = verdictOf(row, req.Type, ref, source)
	}

	// 4. Public resolvers.
	recRows := r.recursive(ctx, ref, source)

	sum := &propSummary{Reference: ref, RefSource: source, Total: len(recRows), Verdicts: map[string]string{}, Groups: []propGroup{}, Vantage: "server"}
	if req.Agent != "" {
		sum.Vantage = req.Agent
	}
	groups := map[string]*propGroup{}
	for _, row := range recRows {
		if row.OK && len(row.Values) > 0 {
			k := strings.Join(row.Values, "\x00")
			if groups[k] == nil {
				groups[k] = &propGroup{Values: row.Values}
			}
			groups[k].Count++
		}
	}
	for _, g := range groups {
		sum.Groups = append(sum.Groups, *g)
	}
	sort.Slice(sum.Groups, func(i, j int) bool {
		if sum.Groups[i].Count != sum.Groups[j].Count {
			return sum.Groups[i].Count > sum.Groups[j].Count
		}
		return strings.Join(sum.Groups[i].Values, ",") < strings.Join(sum.Groups[j].Values, ",")
	})
	if source == "" && len(sum.Groups) > 0 {
		ref, source = sum.Groups[0].Values, "majority"
		sum.Reference, sum.RefSource = ref, source
	}
	if sum.Reference == nil {
		sum.Reference = []string{}
	}
	for _, row := range append(append([]*propRow(nil), authRows...), recRows...) {
		row.Verdict = verdictOf(row, req.Type, ref, source)
		sum.Verdicts[row.ID] = row.Verdict
	}
	for _, row := range recRows {
		if row.OK {
			sum.Responded++
		}
		switch row.Verdict {
		case "match":
			sum.Match++
		case "differ":
			sum.Differ++
		case "empty":
			sum.Empty++
		case "unknown":
			sum.Unknown++
		}
	}
	pct := func(n, of int) float64 {
		if of == 0 {
			return 0
		}
		return math.Round(float64(n)/float64(of)*1000) / 10
	}
	sum.ConfirmPct = pct(sum.Match, sum.Total)
	sum.ComparablePct = pct(sum.Match, sum.Match+sum.Differ+sum.Empty)
	sum.CoveragePct = pct(sum.Responded, sum.Total)
	if source == "authority" || source == "majority" {
		if len(sum.Groups) > 1 {
			r.note("解析器之间结果不同不一定是没生效：CDN、分线路解析会特意给不同的解析器不同的答案")
		}
	}
	if ctx.Err() != nil {
		r.note("检测超过时限，剩下的查询没有做完")
	}
	// Answers in the fake-IP range come from a transparent proxy in front of
	// whoever sent the queries, not from the servers asked.
	for _, row := range append(append([]*propRow(nil), authRows...), recRows...) {
		if hasFakeIP(row.Answers) {
			r.notes = append([]string{"应答里出现了 fake-IP（198.18.0.0/15）：发出查询的一方的 DNS 被透明代理接管，这次检测看到的不是各服务器的真实应答"}, r.notes...)
			break
		}
	}
	sum.Notes = r.notes
	sum.Queries = int(min(r.queries.Load(), propMaxQueries))
	sum.ElapsedMs = time.Since(started).Milliseconds()
	emit(propEvent{Type: "done", Summary: sum})
	return !errors.Is(ctx.Err(), context.Canceled)
}

func hasFakeIP(answers []protocol.DNSAnswer) bool {
	for _, a := range answers {
		if a.Type == "A" && probe.IsFakeIP(net.ParseIP(a.Value)) {
			return true
		}
	}
	return false
}

// agreedValues is what the zone's servers say, when those that answered
// authoritatively all say the same.
func agreedValues(rows []*propRow) ([]string, bool) {
	var ref []string
	seen := false
	for _, row := range rows {
		if !row.OK || !row.AA || row.RCode != "Success" || len(row.Values) == 0 {
			continue
		}
		if seen && strings.Join(ref, "\x00") != strings.Join(row.Values, "\x00") {
			return nil, false
		}
		ref, seen = row.Values, true
	}
	return ref, seen
}

// authority asks every server of the zone for the record and for the zone's
// SOA serial.
func (r *propRun) authority(ctx context.Context, zone string, servers []nsAddr) []*propRow {
	parent := r.nsNames
	if len(parent) == 0 {
		for _, s := range servers {
			parent = append(parent, s.Host)
		}
	}
	r.emit(propEvent{Type: "stage", Stage: &propStage{ID: "authority", State: "running", Total: len(servers), Zone: zoneLabel(zone), NSParent: parent}})
	rows := make([]*propRow, len(servers))
	sem := make(chan struct{}, propConcurrency)
	var wg sync.WaitGroup
	for i, s := range servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			id := "auth:" + s.Host
			if s.Addr == "" {
				rows[i] = r.row("authority", id, s.Host, "", nil, errors.New("查不到这台服务器的 IPv4 地址"))
			} else {
				m, err := r.askTwice(ctx, s.Addr, r.req.Name, r.req.Type, false, propHopTimeout)
				rows[i] = r.row("authority", id, s.Host, s.Addr, m, err)
				if err == nil {
					if soa, err := r.ask(ctx, s.Addr, zoneLabel(zone), "SOA", false, propHopTimeout); err == nil {
						rows[i].Serial = soaSerial(soa.Answers)
					}
				}
			}
			r.emit(propEvent{Type: "row", Row: rows[i]})
		}()
	}
	wg.Wait()

	st := &propStage{ID: "authority", State: "done", Total: len(servers), Zone: zoneLabel(zone), NSParent: parent}
	// The zone's own idea of its name servers, from the first that answers.
	for _, row := range rows {
		if !row.OK {
			continue
		}
		if m, err := r.ask(ctx, row.Addr, zoneLabel(zone), "NS", false, propHopTimeout); err == nil {
			st.NSChild, _ = propValues(m.Answers, "NS")
			break
		}
	}
	answered, serials := 0, map[uint32]bool{}
	for _, row := range rows {
		if row.OK {
			answered++
			if row.Serial != 0 {
				serials[row.Serial] = true
			}
		}
	}
	var notes []string
	notes = append(notes, fmt.Sprintf("%d 台中 %d 台有应答", len(rows), answered))
	if len(serials) > 1 {
		notes = append(notes, "各台的 SOA 序列号不一致（刚改过记录时说明还没同步完；混用多家 DNS 服务商的域名，各家的序列号本来就不同）")
	} else if len(serials) == 1 {
		notes = append(notes, "SOA 序列号一致")
	}
	if len(st.NSChild) > 0 && strings.Join(st.NSChild, ",") != strings.Join(sortedCopy(parent), ",") {
		notes = append(notes, "上级区委派的 NS 与本区自己列出的 NS 不一致")
	}
	if answered == 0 {
		st.State = "failed"
	}
	st.Note = strings.Join(notes, "；")
	r.emit(propEvent{Type: "stage", Stage: st})
	return rows
}

func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func soaSerial(answers []protocol.DNSAnswer) uint32 {
	for _, a := range answers {
		if a.Type != "SOA" {
			continue
		}
		if i := strings.LastIndex(a.Value, "serial="); i >= 0 {
			n, _ := strconv.ParseUint(a.Value[i+len("serial="):], 10, 32)
			return uint32(n)
		}
	}
	return 0
}

// recursive asks the directory's public resolvers, from the server or from
// the chosen node.
func (r *propRun) recursive(ctx context.Context, ref []string, source string) []*propRow {
	targets := r.c.targets(r.req.Tier)
	note := "从服务器所在网络发出查询"
	if r.req.Agent != "" {
		note = "由节点 " + r.req.Agent + " 发出查询"
	}
	r.emit(propEvent{Type: "stage", Stage: &propStage{ID: "recursive", State: "running", Total: len(targets), Note: note}})
	rows := make([]*propRow, len(targets))
	finish := func(i int, m *probe.DNSMessage, err error) {
		t := targets[i]
		row := r.row("recursive", "rec:"+t.addr, t.res.Name, t.addr, m, err)
		row.Org, row.CC = t.res.Org, t.res.CC
		row.Verdict = verdictOf(row, r.req.Type, ref, source)
		rows[i] = row
		r.emit(propEvent{Type: "row", Row: row})
	}
	if r.req.Agent != "" {
		r.viaAgent(ctx, targets, finish)
	} else {
		sem := make(chan struct{}, propConcurrency)
		var wg sync.WaitGroup
		for i, t := range targets {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				m, err := r.askTwice(ctx, t.addr, r.req.Name, r.req.Type, true, propAskTimeout)
				finish(i, m, err)
			}()
		}
		wg.Wait()
	}
	answered := 0
	for _, row := range rows {
		if row.OK {
			answered++
		}
	}
	st := &propStage{ID: "recursive", State: "done", Total: len(targets), Note: fmt.Sprintf("%s；%d 个地址中 %d 个有应答", note, len(rows), answered)}
	if answered == 0 && len(rows) > 0 {
		st.State = "failed"
	}
	r.emit(propEvent{Type: "stage", Stage: st})
	return rows
}

// viaAgent runs the recursive step on a node: the addresses go out as
// dns_compare tasks (which take a handful of resolvers each) and the entries
// that come back become rows.
func (r *propRun) viaAgent(ctx context.Context, targets []propTarget, finish func(int, *probe.DNSMessage, error)) {
	const per = 8 // what one dns_compare task accepts
	var wg sync.WaitGroup
	for start := 0; start < len(targets); start += per {
		end := min(start+per, len(targets))
		wg.Add(1)
		go func() {
			defer wg.Done()
			fail := func(err error) {
				for i := start; i < end; i++ {
					finish(i, nil, err)
				}
			}
			if r.hub == nil {
				fail(errors.New("节点不可用"))
				return
			}
			servers := make([]string, 0, end-start)
			for _, t := range targets[start:end] {
				servers = append(servers, t.addr)
			}
			ev, err := r.hub.CreateTask(protocol.TaskDNSCompare, r.req.Name, protocol.Params{RecordType: r.req.Type, DNSServers: servers, TimeoutMs: int(propAskTimeout / time.Millisecond)}, []string{r.req.Agent}, r.req.owner)
			if err != nil {
				fail(err)
				return
			}
			res := r.waitResult(ctx, ev.Task.ID)
			if res == nil {
				fail(errors.New("节点没有在时限内返回结果"))
				return
			}
			var data protocol.DNSCompareResult
			if res.Error != "" && len(res.Data) == 0 {
				fail(errors.New(res.Error))
				return
			}
			if err := json.Unmarshal(res.Data, &data); err != nil {
				fail(errors.New("节点返回的数据无法解析"))
				return
			}
			byServer := map[string]protocol.DNSAttempt{}
			for _, e := range data.Entries {
				byServer[e.Server] = e.Attempt
			}
			for i := start; i < end; i++ {
				att, ok := byServer[targets[i].addr]
				switch {
				case !ok:
					finish(i, nil, errors.New("节点没有返回这个地址的结果"))
				case att.RCode == "" && att.Error != "":
					finish(i, nil, errors.New(att.Error))
				default:
					finish(i, &probe.DNSMessage{RCode: att.RCode, RTTMs: att.RTTMs, Answers: att.Answers}, nil)
				}
			}
		}()
	}
	wg.Wait()
}

// waitResult waits for the single agent's result of a task.
func (r *propRun) waitResult(ctx context.Context, taskID string) *protocol.AgentResult {
	snap, events, unsub, ok := r.hub.Subscribe(taskID)
	if !ok {
		return nil
	}
	defer unsub()
	finished := func(res *protocol.AgentResult) bool {
		return res != nil && res.Status != protocol.StatusRunning && res.Status != protocol.StatusPending
	}
	for _, res := range snap.Results {
		if finished(res) {
			return res
		}
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, open := <-events:
			if !open {
				return nil
			}
			if ev.Type == "result" && finished(ev.Result) {
				return ev.Result
			}
			if ev.Type == "done" {
				if loaded, err := r.hub.LoadTask(taskID); err == nil {
					for _, res := range loaded.Results {
						if finished(res) {
							return res
						}
					}
				}
				return nil
			}
		}
	}
}

// --- HTTP ---

func (c *propChecker) cached(key string) []propEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range c.cache {
		if time.Since(v.at) > propCacheTTL {
			delete(c.cache, k)
		}
	}
	return c.cache[key].events
}

func (c *propChecker) store(key string, events []propEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[key] = propCached{at: time.Now(), events: events}
}

// dnsPropagation streams one check as server-sent events.
func (a *API) dnsPropagation(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req, err := parsePropRequest(q.Get("name"), q.Get("type"), q.Get("tier"), q.Get("expect"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	admin := a.auth.isAdmin(r)
	if agent := strings.TrimSpace(q.Get("agent")); agent != "" {
		if !admin {
			writeErr(w, http.StatusForbidden, "只有管理员可以指定观测节点")
			return
		}
		req.Agent, req.owner = agent, a.auth.owner(r)
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	var mu sync.Mutex
	var sent []propEvent
	write := func(ev propEvent) {
		b, err := json.Marshal(ev)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, b)
		flusher.Flush()
	}
	headers := func() {
		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-store")
		h.Set("Connection", "keep-alive")
		h.Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
	}
	key := req.key()
	if events := a.prop.cached(key); events != nil {
		headers()
		for _, ev := range events {
			write(ev)
		}
		return
	}
	if !admin && !a.prop.limiter.allow(a.cfg.ClientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, "检测过于频繁，每分钟最多 3 次，请稍后再试")
		return
	}
	select {
	case a.prop.slots <- struct{}{}:
		defer func() { <-a.prop.slots }()
	default:
		writeErr(w, http.StatusTooManyRequests, "同时进行的扩散检测已满，请稍后再试")
		return
	}
	headers()
	ctx, cancel := context.WithTimeout(r.Context(), propDeadline)
	defer cancel()
	complete := a.prop.run(ctx, req, a.hub, func(ev propEvent) {
		mu.Lock()
		defer mu.Unlock()
		sent = append(sent, ev)
		write(ev)
	})
	if complete {
		a.prop.store(key, sent)
	}
}
