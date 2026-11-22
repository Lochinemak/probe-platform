package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"probe-platform/internal/probe"
	"probe-platform/internal/protocol"
)

// fakeDNS is a root → TLD → zone hierarchy and a few resolvers, in memory:
// what each address answers is a function of the question.
type fakeDNS struct {
	mu      sync.Mutex
	asked   map[string]int // address → questions it got
	servers map[string]func(q probe.DNSQuestion) (*probe.DNSMessage, error)
}

func (f *fakeDNS) exchange(_ context.Context, q probe.DNSQuestion) (*probe.DNSMessage, error) {
	host, port, _ := net.SplitHostPort(q.Server)
	f.mu.Lock()
	f.asked[host]++
	f.mu.Unlock()
	if port != "53" {
		return nil, errors.New("asked a port other than 53")
	}
	h := f.servers[host]
	if h == nil {
		return nil, errors.New("timeout")
	}
	return h(q)
}

func (f *fakeDNS) count(addr string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.asked[addr]
}

func rr(name, typ, value string) protocol.DNSAnswer {
	return protocol.DNSAnswer{Name: name, Type: typ, TTL: 300, Value: value}
}

const (
	fRoot = "198.41.0.4"
	fTLD  = "9.9.0.1"
	fNS1  = "9.9.1.1"
	fNS2  = "9.9.1.2"
	fNS3  = "9.9.1.3" // ns3.other.test: no glue, found through the system resolver
	fR1   = "8.8.1.1"
	fR2   = "8.8.1.2"
	fR3   = "8.8.1.3" // never answers
)

// newFakeDNS serves www.example.test. ns3 and resolver 2 still hold the old
// address when stale is set.
func newFakeDNS(stale bool) *fakeDNS {
	f := &fakeDNS{asked: map[string]int{}, servers: map[string]func(probe.DNSQuestion) (*probe.DNSMessage, error){}}
	const newA, oldA = "203.0.113.10", "203.0.113.99"
	f.servers[fRoot] = func(probe.DNSQuestion) (*probe.DNSMessage, error) {
		return &probe.DNSMessage{RCode: "Success", RTTMs: 1,
			Authority:  []protocol.DNSAnswer{rr("test", "NS", "ns.tld.test")},
			Additional: []protocol.DNSAnswer{rr("ns.tld.test", "A", fTLD)}}, nil
	}
	f.servers[fTLD] = func(q probe.DNSQuestion) (*probe.DNSMessage, error) {
		if !strings.HasSuffix(normName(q.Name), "example.test") {
			return &probe.DNSMessage{RCode: "NameError", Authoritative: true, RTTMs: 1}, nil
		}
		return &probe.DNSMessage{RCode: "Success", RTTMs: 2,
			Authority: []protocol.DNSAnswer{rr("example.test", "NS", "ns1.example.test."), rr("example.test", "NS", "NS2.example.test"), rr("example.test", "NS", "ns3.other.test")},
			Additional: []protocol.DNSAnswer{rr("ns1.example.test", "A", fNS1), rr("ns2.example.test", "A", fNS2),
				rr("ns2.example.test", "AAAA", "2001:db8::2")}}, nil
	}
	auth := func(a string, serial string) func(probe.DNSQuestion) (*probe.DNSMessage, error) {
		return func(q probe.DNSQuestion) (*probe.DNSMessage, error) {
			if q.Recurse {
				return nil, errors.New("an authoritative server was asked to recurse")
			}
			m := &probe.DNSMessage{RCode: "Success", Authoritative: true, RTTMs: 3}
			switch q.Type {
			case "SOA":
				soa := rr("example.test", "SOA", "ns1.example.test hostmaster.example.test serial="+serial)
				if normName(q.Name) == "example.test" {
					m.Answers = []protocol.DNSAnswer{soa}
				} else {
					m.Authority = []protocol.DNSAnswer{soa}
				}
			case "NS":
				m.Answers = []protocol.DNSAnswer{rr("example.test", "NS", "ns1.example.test"), rr("example.test", "NS", "ns2.example.test"), rr("example.test", "NS", "ns3.other.test")}
			default:
				m.Answers = []protocol.DNSAnswer{rr("www.example.test", "A", a)}
			}
			return m, nil
		}
	}
	f.servers[fNS1], f.servers[fNS2], f.servers[fNS3] = auth(newA, "2"), auth(newA, "2"), auth(newA, "2")
	rec := func(a string) func(probe.DNSQuestion) (*probe.DNSMessage, error) {
		return func(q probe.DNSQuestion) (*probe.DNSMessage, error) {
			if !q.Recurse {
				return nil, errors.New("a resolver was asked without RD")
			}
			return &probe.DNSMessage{RCode: "Success", RTTMs: 9, Answers: []protocol.DNSAnswer{rr("www.example.test", "A", a)}}, nil
		}
	}
	f.servers[fR1], f.servers[fR2] = rec(newA), rec(newA)
	if stale {
		f.servers[fNS3], f.servers[fR2] = auth(oldA, "1"), rec(oldA)
	}
	return f
}

func (f *fakeDNS) checker() *propChecker {
	c := newPropChecker(nil)
	c.exchange = f.exchange
	c.roots = []nsAddr{{"a.root-servers.net", fRoot}}
	c.lookupHost = func(_ context.Context, host string) ([]string, error) {
		if host == "ns3.other.test" {
			return []string{fNS3}, nil
		}
		return nil, errors.New("no such host")
	}
	c.resolvers = []Resolver{
		{ID: "r1", Name: "R1", Org: "o", CC: "CN", Tier: "quick", IPv4: []string{fR1}},
		{ID: "r2", Name: "R2", Org: "o", CC: "US", Tier: "quick", IPv4: []string{fR2}},
		{ID: "r3", Name: "R3", Org: "o", CC: "US", Tier: "full", IPv4: []string{fR3}},
		{ID: "isp", Name: "ISP", Org: "o", CC: "CN", Tier: "dir", IPv4: []string{"8.8.1.9"}},
	}
	return c
}

type propOutcome struct {
	stages map[string]*propStage // last state of each
	rows   map[string]*propRow
	sum    *propSummary
}

func runProp(t *testing.T, c *propChecker, req propRequest) propOutcome {
	t.Helper()
	out := propOutcome{stages: map[string]*propStage{}, rows: map[string]*propRow{}}
	var mu sync.Mutex
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c.run(ctx, req, nil, func(ev propEvent) {
		mu.Lock()
		defer mu.Unlock()
		switch ev.Type {
		case "stage":
			out.stages[ev.Stage.ID] = ev.Stage
		case "row":
			out.rows[ev.Row.ID] = ev.Row
		case "done":
			out.sum = ev.Summary
		}
	})
	if out.sum == nil {
		t.Fatal("no done event")
	}
	return out
}

func TestPropagationAgainstFakeHierarchy(t *testing.T) {
	f := newFakeDNS(false)
	req, err := parsePropRequest("WWW.Example.test.", "a", "full", "")
	if err != nil {
		t.Fatal(err)
	}
	out := runProp(t, f.checker(), req)

	d := out.stages["delegation"]
	if d.State != "done" || d.Zone != "example.test." || len(d.Hops) != 3 {
		t.Fatalf("delegation: %+v", d)
	}
	if h := d.Hops; h[0].Result != "referral" || h[0].Next != "test." || h[1].Result != "referral" || h[1].Next != "example.test." || len(h[1].NS) != 3 || h[2].Result != "answer" {
		t.Fatalf("hops: %+v", d.Hops)
	}
	a := out.stages["authority"]
	if a.State != "done" || len(a.NSParent) != 3 || len(a.NSChild) != 3 || !strings.Contains(a.Note, "SOA 序列号一致") || strings.Contains(a.Note, "不一致") {
		t.Fatalf("authority: %+v", a)
	}
	for _, host := range []string{"ns1.example.test", "ns2.example.test", "ns3.other.test"} {
		row := out.rows["auth:"+host]
		if row == nil || !row.OK || !row.AA || row.Serial != 2 || len(row.Values) != 1 || row.Values[0] != "203.0.113.10" {
			t.Fatalf("authority row %s: %+v", host, row)
		}
	}
	// The directory-only resolver is not asked; the one that is down counts
	// as "cannot tell", not as a mismatch.
	if f.count("8.8.1.9") != 0 || len(out.rows) != 6 {
		t.Fatalf("rows %d, directory-only resolver asked %d times", len(out.rows), f.count("8.8.1.9"))
	}
	if f.count(fR3) != 2 {
		t.Errorf("a silent resolver should be asked twice, was asked %d times", f.count(fR3))
	}
	s := out.sum
	if s.RefSource != "authority" || len(s.Reference) != 1 || s.Total != 3 || s.Responded != 2 || s.Match != 2 || s.Differ != 0 || s.Unknown != 1 {
		t.Fatalf("summary: %+v", s)
	}
	if s.ConfirmPct != 66.7 || s.ComparablePct != 100 || s.CoveragePct != 66.7 || s.Verdicts["rec:"+fR3] != "unknown" || s.Verdicts["auth:ns1.example.test"] != "match" {
		t.Fatalf("metrics: %+v", s)
	}

	// Quick tier leaves the "full" resolver out.
	req.Tier = "quick"
	if out := runProp(t, f.checker(), req); out.sum.Total != 2 {
		t.Fatalf("quick tier asked %d resolvers", out.sum.Total)
	}
}

func TestPropagationHalfDone(t *testing.T) {
	f := newFakeDNS(true)
	req, _ := parsePropRequest("www.example.test", "A", "full", "")
	out := runProp(t, f.checker(), req)
	// The zone's servers disagree, so they cannot be the reference; the
	// majority of the resolvers is.
	if a := out.stages["authority"]; !strings.Contains(a.Note, "SOA 序列号不一致") {
		t.Fatalf("authority note: %q", a.Note)
	}
	if s := out.sum; s.RefSource != "majority" || len(s.Groups) != 2 || s.Match != 1 || s.Differ != 1 || s.Unknown != 1 {
		t.Fatalf("summary: %+v", s)
	}

	// With an expectation, each server is measured against it.
	req, _ = parsePropRequest("www.example.test", "A", "full", " 203.0.113.10 ")
	out = runProp(t, f.checker(), req)
	s := out.sum
	if s.RefSource != "expect" || s.Match != 1 || s.Differ != 1 || s.ComparablePct != 50 || s.ConfirmPct != 33.3 {
		t.Fatalf("summary with expectation: %+v", s)
	}
	if s.Verdicts["auth:ns3.other.test"] != "differ" || s.Verdicts["auth:ns1.example.test"] != "match" || s.Verdicts["rec:"+fR2] != "differ" {
		t.Fatalf("verdicts: %+v", s.Verdicts)
	}
	if out.stages["expect"].State != "done" {
		t.Fatalf("expect stage: %+v", out.stages["expect"])
	}
}

// A zone may point its NS records at anything. Addresses that are not on the
// public internet are never asked.
func TestPropagationRefusesPrivateNS(t *testing.T) {
	for _, private := range []string{"10.0.0.5", "127.0.0.1", "192.168.1.1", "169.254.169.254"} {
		f := newFakeDNS(false)
		f.servers[fTLD] = func(probe.DNSQuestion) (*probe.DNSMessage, error) {
			return &probe.DNSMessage{RCode: "Success",
				Authority:  []protocol.DNSAnswer{rr("example.test", "NS", "ns1.example.test")},
				Additional: []protocol.DNSAnswer{rr("ns1.example.test", "A", private)}}, nil
		}
		f.servers[private] = func(probe.DNSQuestion) (*probe.DNSMessage, error) {
			return &probe.DNSMessage{RCode: "Success", Authoritative: true}, nil
		}
		req, _ := parsePropRequest("www.example.test", "A", "quick", "")
		out := runProp(t, f.checker(), req)
		if f.count(private) != 0 {
			t.Fatalf("%s was asked %d times", private, f.count(private))
		}
		if d := out.stages["delegation"]; d.State != "failed" {
			t.Fatalf("%s: delegation %+v", private, d)
		}
		if a := out.stages["authority"]; a.State != "skipped" {
			t.Fatalf("%s: authority %+v", private, a)
		}
		// The resolvers are still asked.
		if out.sum.Total != 2 || out.sum.Match != 2 || out.sum.RefSource != "majority" {
			t.Fatalf("%s: summary %+v", private, out.sum)
		}
	}
}

func TestPropagationOddAndMissing(t *testing.T) {
	// A root that answers with data it is not authoritative for and no
	// referral: a forged reply. Not followed.
	f := newFakeDNS(false)
	f.servers[fRoot] = func(probe.DNSQuestion) (*probe.DNSMessage, error) {
		return &probe.DNSMessage{RCode: "Success", Answers: []protocol.DNSAnswer{rr("www.example.test", "A", "1.2.3.4")}}, nil
	}
	req, _ := parsePropRequest("www.example.test", "A", "quick", "")
	out := runProp(t, f.checker(), req)
	if d := out.stages["delegation"]; d.State != "failed" || len(d.Hops) != 1 || d.Hops[0].Result != "odd" {
		t.Fatalf("delegation: %+v", d)
	}

	// A name the TLD does not know.
	f = newFakeDNS(false)
	req, _ = parsePropRequest("nothing.test", "A", "quick", "")
	out = runProp(t, f.checker(), req)
	if d := out.stages["delegation"]; d.State != "done" || d.Zone != "test." || !strings.Contains(d.Note, "NXDOMAIN") {
		t.Fatalf("delegation of a missing name: %+v", d)
	}
}

func TestPropagationBudget(t *testing.T) {
	f := newFakeDNS(false)
	c := f.checker()
	for i := 0; i < 200; i++ {
		c.resolvers = append(c.resolvers, Resolver{ID: "x", Name: "x", Tier: "quick", IPv4: []string{fmt.Sprintf("8.9.%d.%d", i/200+1, i%200+1)}})
	}
	req, _ := parsePropRequest("www.example.test", "A", "quick", "")
	out := runProp(t, c, req)
	total := 0
	f.mu.Lock()
	for _, n := range f.asked {
		total += n
	}
	f.mu.Unlock()
	if total > propMaxQueries || out.sum.Queries != propMaxQueries {
		t.Fatalf("%d questions were sent, summary says %d", total, out.sum.Queries)
	}
}

func TestParsePropRequest(t *testing.T) {
	for _, bad := range [][2]string{{"", "A"}, {"exa mple.com", "A"}, {"example.com/path", "A"}, {"-bad.example.com", "A"}, {"example.com", "ANY"},
		{"1.2.3.4", "A"}, {strings.Repeat("a", 64) + ".com", "A"}, {"例子.com", "A"}} {
		if _, err := parsePropRequest(bad[0], bad[1], "", ""); err == nil {
			t.Errorf("%q %s accepted", bad[0], bad[1])
		}
	}
	for in, want := range map[string]string{"_dmarc.Example.COM.": "_dmarc.example.com", "xn--fsq.com": "xn--fsq.com", "com": "com"} {
		if req, err := parsePropRequest(in, "", "", ""); err != nil || req.Name != want || req.Type != "A" || req.Tier != "quick" {
			t.Errorf("%q: %+v %v", in, req, err)
		}
	}
	if req, err := parsePropRequest("8.8.4.4", "ptr", "full", ""); err != nil || req.Name != "4.4.8.8.in-addr.arpa" || req.Tier != "full" {
		t.Errorf("PTR of an address: %+v %v", req, err)
	}
	if got := reverseName(net.ParseIP("2001:db8::1")); got != "1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.8.b.d.0.1.0.0.2.ip6.arpa" {
		t.Errorf("ip6.arpa: %s", got)
	}
	if v := expectValues("MX", "10 Mail.Example.com., 20 mx2.example.com"); len(v) != 2 || v[0] != "10 mail.example.com" {
		t.Errorf("expected values: %q", v)
	}
	row := &propRow{OK: true, RCode: "Success", Values: []string{"v=spf1 include:_spf.example.com ~all"}}
	if verdictOf(row, "TXT", expectValues("TXT", "include:_spf.example.com"), "expect") != "match" {
		t.Error("a TXT expectation should match a part of the record")
	}
	if verdictOf(&propRow{OK: true, RCode: "ServerFailure"}, "A", []string{"1.1.1.1"}, "expect") != "unknown" {
		t.Error("SERVFAIL should be undecidable")
	}
	if verdictOf(&propRow{OK: true, RCode: "NameError", Values: []string{}}, "A", []string{"1.1.1.1"}, "expect") != "empty" {
		t.Error("NXDOMAIN should count as an empty answer")
	}
}

// sseEvents reads a whole event stream.
func sseEvents(t *testing.T, c *http.Client, u string) (int, []propEvent) {
	t.Helper()
	resp, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out []propEvent
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			var ev propEvent
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				t.Fatalf("event %q: %v", data, err)
			}
			out = append(out, ev)
		}
	}
	return resp.StatusCode, out
}

func useFakeDNS(t *testing.T, f *fakeDNS) {
	t.Helper()
	oe, or, ores, ol := propExchange, propRoots, propResolvers, propLookupHost
	c := f.checker()
	propExchange, propRoots, propResolvers, propLookupHost = f.exchange, c.roots, func() []Resolver { return c.resolvers }, c.lookupHost
	t.Cleanup(func() { propExchange, propRoots, propResolvers, propLookupHost = oe, or, ores, ol })
}

func TestPropagationAPI(t *testing.T) {
	f := newFakeDNS(false)
	useFakeDNS(t, f)
	srv, _, _, admin := newShareEnv(t, true)
	jar, _ := cookiejar.New(nil)
	guest := &http.Client{Jar: jar}
	base := srv.URL + "/api/dns/propagation?"

	if code := doJSON(t, guest, "GET", base+"name=bad%20name", "", nil); code != http.StatusBadRequest {
		t.Fatalf("bad name: %d", code)
	}
	if code := doJSON(t, guest, "GET", base+"name=www.example.test&agent=n1", "", nil); code != http.StatusForbidden {
		t.Fatalf("a guest chose a node: %d", code)
	}

	q := url.Values{"name": {"www.example.test"}, "type": {"A"}, "tier": {"full"}}.Encode()
	code, events := sseEvents(t, guest, base+q)
	if code != 200 || len(events) == 0 || events[0].Type != "start" || events[len(events)-1].Type != "done" {
		t.Fatalf("stream: %d, %d events", code, len(events))
	}
	sum := events[len(events)-1].Summary
	if sum.RefSource != "authority" || sum.Match != 2 || sum.Total != 3 {
		t.Fatalf("summary: %+v", sum)
	}
	// The same check again within the cache window asks nothing.
	before := f.count(fRoot)
	if code, again := sseEvents(t, guest, base+q); code != 200 || len(again) != len(events) || f.count(fRoot) != before {
		t.Fatalf("cached replay: %d, %d events, root asked %d → %d", code, len(again), before, f.count(fRoot))
	}

	// Three runs a minute for a visitor; the cached one did not count.
	for i, name := range []string{"a.example.test", "b.example.test", "c.example.test"} {
		code, _ := sseEvents(t, guest, base+"name="+name)
		if want := map[bool]int{true: 200, false: http.StatusTooManyRequests}[i < 2]; code != want {
			t.Fatalf("run %d: %d, want %d", i+2, code, want)
		}
	}
	if code, _ := sseEvents(t, admin, base+"name=d.example.test"); code != 200 {
		t.Fatalf("the administrator is not rate-limited: %d", code)
	}

	// With guest access off the endpoint needs a login.
	srv2, _, _, _ := newShareEnv(t, false)
	if code := doJSON(t, guest, "GET", srv2.URL+"/api/dns/propagation?name=www.example.test", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("guest access off: %d", code)
	}
}

// The recursive step can run on a node: the addresses go out as dns_compare
// tasks and the entries become rows.
func TestPropagationViaAgent(t *testing.T) {
	f := newFakeDNS(false)
	useFakeDNS(t, f)
	e := newTokenEnv(t)
	var created struct {
		Token string `json:"token"`
	}
	if code := e.call("POST", "/api/agents", `{"name":"hk"}`, &created); code != 201 {
		t.Fatalf("create node: %d", code)
	}
	conn, _, code, reason := e.dial(created.Token, protocol.Hello{Name: "hk", Capabilities: []string{"tcp", "dns_multi"}})
	if conn == nil {
		t.Fatalf("dial: %d %s", code, reason)
	}
	defer conn.Close()

	go func() {
		m := readMsg(t, conn, protocol.MsgTask)
		var task protocol.Task
		_ = json.Unmarshal(m.Payload, &task)
		res := protocol.DNSCompareResult{Target: task.Target, RecordType: task.Params.RecordType}
		for _, s := range task.Params.DNSServers {
			entry := protocol.DNSCompareEntry{Server: s, Attempt: protocol.DNSAttempt{OK: true, RCode: "Success", RTTMs: 30,
				Answers: []protocol.DNSAnswer{rr("www.example.test", "A", "203.0.113.10")}}}
			if s == fR3 {
				entry.Attempt = protocol.DNSAttempt{Error: "timeout", Answers: []protocol.DNSAnswer{}}
			}
			res.Entries = append(res.Entries, entry)
		}
		msg, _ := protocol.NewMessage(protocol.MsgResult, protocol.Result{TaskID: task.ID, OK: true, Data: mustJSON(res)})
		_ = conn.WriteJSON(msg)
	}()

	code, events := sseEvents(t, e.admin, e.srv.URL+"/api/dns/propagation?name=www.example.test&tier=full&agent=hk")
	if code != 200 || len(events) == 0 || events[len(events)-1].Type != "done" {
		t.Fatalf("stream: %d, %d events", code, len(events))
	}
	sum := events[len(events)-1].Summary
	if sum.Vantage != "hk" || sum.Total != 3 || sum.Match != 2 || sum.Unknown != 1 || sum.Responded != 2 {
		t.Fatalf("summary: %+v", sum)
	}
	// The server asked the hierarchy itself but none of the resolvers.
	if f.count(fR1)+f.count(fR2)+f.count(fR3) != 0 || f.count(fRoot) == 0 {
		t.Fatalf("resolvers asked from the server: %d %d %d", f.count(fR1), f.count(fR2), f.count(fR3))
	}
}

// One set of servers may hold a zone and its child and answer for the child
// without a referral; the SOA tells which zone the name is in. And answers
// from the fake-IP range are called out.
func TestPropagationApexAndFakeIP(t *testing.T) {
	f := newFakeDNS(false)
	f.servers[fTLD] = func(q probe.DNSQuestion) (*probe.DNSMessage, error) {
		m := &probe.DNSMessage{RCode: "Success", Authoritative: true}
		if q.Type == "SOA" {
			m.Authority = []protocol.DNSAnswer{rr("example.test", "SOA", "ns.tld.test hostmaster.tld.test serial=7")}
		} else {
			m.Answers = []protocol.DNSAnswer{rr("www.example.test", "A", "198.18.0.7")}
		}
		return m, nil
	}
	req, _ := parsePropRequest("www.example.test", "A", "quick", "")
	out := runProp(t, f.checker(), req)
	if d := out.stages["delegation"]; d.State != "done" || d.Zone != "example.test." {
		t.Fatalf("zone: %+v", d)
	}
	if len(out.sum.Notes) == 0 || !strings.Contains(out.sum.Notes[0], "fake-IP") {
		t.Fatalf("notes: %q", out.sum.Notes)
	}
}
