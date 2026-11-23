package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"probe-platform/internal/protocol"
)

func TestParseRegion(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want protocol.Geo
	}{
		// v3 layout, as shipped in data/ip2region.xdb.
		{"中国|广东省|深圳市|电信|CN", protocol.Geo{CC: "CN", Country: "中国", Province: "广东", City: "深圳", Line: "电信"}},
		{"中国|北京|北京市|联通|CN", protocol.Geo{CC: "CN", Country: "中国", Province: "北京", City: "北京", Line: "联通"}},
		{"中国|内蒙古|呼和浩特市|移动|CN", protocol.Geo{CC: "CN", Country: "中国", Province: "内蒙古", City: "呼和浩特", Line: "移动"}},
		{"中国|香港特别行政区|0|0|CN", protocol.Geo{CC: "CN", Country: "中国", Province: "香港"}},
		{"中国|上海|上海市|腾讯云|CN", protocol.Geo{CC: "CN", Country: "中国", Province: "上海", City: "上海", Line: "云厂商"}},
		{"United States|California|Los Angeles|0|US", protocol.Geo{CC: "US", Country: "美国", Province: "California", City: "Los Angeles"}},
		{"Italy|Lazio|Rome|Telecom Italia|IT", protocol.Geo{CC: "IT", Country: "意大利", Province: "Lazio", City: "Rome", Line: "其他"}},
		{"Hong Kong|0|0|HKT|HK", protocol.Geo{CC: "CN", Country: "中国", Province: "香港", Line: "其他"}},
		// v2 layout: 国家|区域|省份|城市|ISP.
		{"中国|0|广东省|深圳市|电信", protocol.Geo{CC: "CN", Country: "中国", Province: "广东", City: "深圳", Line: "电信"}},
		{"日本|0|东京都|东京|0", protocol.Geo{CC: "JP", Country: "日本", Province: "东京都", City: "东京"}},
		{"0|0|0|内网IP|0", protocol.Geo{}},
		{"", protocol.Geo{}},
	} {
		if got := parseRegion(c.raw); got != c.want {
			t.Errorf("parseRegion(%q) = %+v, want %+v", c.raw, got, c.want)
		}
	}
}

func TestLineOf(t *testing.T) {
	for _, c := range []struct{ isp, cc, want string }{
		{"电信", "CN", LineTelecom}, {"中国联通", "", LineUnicom}, {"移动", "CN", LineMobile}, {"铁通", "CN", LineMobile},
		{"中国广电", "CN", LineBroadcast}, {"教育网", "CN", LineEducation},
		{"China Telecom", "CN", LineTelecom}, {"CHINA UNICOM China169 Backbone", "CN", LineUnicom}, {"China Mobile", "CN", LineMobile},
		// The carriers' English names are ordinary words abroad.
		{"Telecom Italia", "IT", LineOther}, {"T-Mobile USA", "US", LineOther}, {"China Telecom", "", LineOther},
		{"腾讯云", "CN", LineCloud}, {"Alibaba Cloud", "SG", LineCloud}, {"Amazon.com", "US", LineCloud},
		// As ip2region spells them.
		{"阿里", "CN", LineCloud}, {"腾讯", "CN", LineCloud}, {"Google LLC", "US", LineCloud}, {"中华电信", "CN", LineOther},
		{"DigitalOcean", "US", LineIDC}, {"BGP 多线", "CN", LineIDC},
		{"开发", "", LineOther}, {"", "CN", ""}, {"0", "CN", ""},
	} {
		if got := lineOf(c.isp, c.cc); got != c.want {
			t.Errorf("lineOf(%q, %q) = %q, want %q", c.isp, c.cc, got, c.want)
		}
	}
}

func TestParseLabel(t *testing.T) {
	cn := func(p, c string) protocol.Geo { return protocol.Geo{CC: "CN", Country: "中国", Province: p, City: c} }
	for _, c := range []struct {
		in   string
		want protocol.Geo
		ok   bool
	}{
		{"广东 深圳", cn("广东", "深圳"), true},
		{"广东深圳", cn("广东", "深圳"), true},
		{"中国 广东省 深圳市", cn("广东", "深圳"), true},
		{"广东电信", cn("广东", ""), true},
		{"深圳", cn("广东", "深圳"), true},
		{"腾讯云 广州", cn("广东", "广州"), true},
		{"上海", cn("上海", ""), true},
		{"内蒙古 包头", cn("内蒙古", "包头"), true},
		{"香港", cn("香港", ""), true},
		{"HK", cn("香港", ""), true},
		{"Shanghai", cn("上海", ""), true},
		{"日本 东京", protocol.Geo{CC: "JP", Country: "日本", City: "东京"}, true},
		{"美国 加利福尼亚州 洛杉矶", protocol.Geo{CC: "US", Country: "美国", Province: "加利福尼亚州", City: "洛杉矶"}, true},
		{"US-LA", protocol.Geo{CC: "US", Country: "美国", City: "LA"}, true},
		{"United States California", protocol.Geo{CC: "US", Country: "美国", City: "California"}, true},
		{"Russia", protocol.Geo{CC: "RU", Country: "俄罗斯"}, true},
		{"爱沙尼亚 塔林", protocol.Geo{CC: "EE", Country: "爱沙尼亚", City: "塔林"}, true},
		{"Estonia", protocol.Geo{CC: "EE", Country: "爱沙尼亚"}, true},
		{"EE Tallinn", protocol.Geo{CC: "EE", Country: "爱沙尼亚", City: "Tallinn"}, true},
		{"印度尼西亚 雅加达", protocol.Geo{CC: "ID", Country: "印度尼西亚", City: "雅加达"}, true},
		{"美国 马里兰", protocol.Geo{CC: "US", Country: "美国", City: "马里兰"}, true},
		{"South Sudan", protocol.Geo{CC: "SS", Country: "南苏丹"}, true},
		{"Papua New Guinea", protocol.Geo{CC: "PG", Country: "巴布亚新几内亚"}, true},
		{"US New Jersey", protocol.Geo{CC: "US", Country: "美国", City: "New Jersey"}, true},
		{"Tallin", protocol.Geo{}, false},
		{"本机", protocol.Geo{}, false},
		{"内网", protocol.Geo{}, false},
		{"home nas", protocol.Geo{}, false},
		{"", protocol.Geo{}, false},
	} {
		got, ok := parseLabel(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("parseLabel(%q) = %+v %v, want %+v %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// TestGeoPrecedence: lookup, then the operator's labels, then the
// administrator's correction, each overriding only what it knows.
func TestGeoPrecedence(t *testing.T) {
	lookup := &protocol.Geo{CC: "CN", Country: "中国", Province: "广东", City: "深圳", Line: "电信"}
	check := func(name string, a protocol.AgentStatus, want *protocol.Geo) {
		t.Helper()
		got := agentGeo(&a)
		if (got == nil) != (want == nil) || (got != nil && *got != *want) {
			t.Errorf("%s: %+v, want %+v", name, got, want)
		}
	}
	check("nothing known", protocol.AgentStatus{Location: "本机", GeoLocation: "内网"}, nil)
	check("lookup only", protocol.AgentStatus{GeoIP: lookup},
		&protocol.Geo{CC: "CN", Country: "中国", Province: "广东", City: "深圳", Line: "电信", Src: "geoip"})
	check("record from before structured lookups", protocol.AgentStatus{GeoLocation: "中国 广东省 深圳市", GeoISP: "电信"},
		&protocol.Geo{CC: "CN", Country: "中国", Province: "广东", City: "深圳", Line: "电信", Src: "geoip"})
	check("label names the same province: keep the looked-up city", protocol.AgentStatus{GeoIP: lookup, Location: "广东"},
		&protocol.Geo{CC: "CN", Country: "中国", Province: "广东", City: "深圳", Line: "电信", Src: "label"})
	check("label names another place: the node is behind a tunnel", protocol.AgentStatus{GeoIP: lookup, Location: "浙江 杭州", ISP: "移动"},
		&protocol.Geo{CC: "CN", Country: "中国", Province: "浙江", City: "杭州", Line: "移动", Src: "label"})
	check("unreadable labels change nothing", protocol.AgentStatus{GeoIP: lookup, Location: "书房", ISP: "家宽"},
		&protocol.Geo{CC: "CN", Country: "中国", Province: "广东", City: "深圳", Line: "电信", Src: "geoip"})
	check("an ISP label with no lookup still gives a line", protocol.AgentStatus{Location: "本机", ISP: "开发"},
		&protocol.Geo{Line: "其他", Src: "label"})
	check("correction of the line only", protocol.AgentStatus{GeoIP: lookup, GeoOverride: &protocol.Geo{Line: "IDC"}},
		&protocol.Geo{CC: "CN", Country: "中国", Province: "广东", City: "深圳", Line: "IDC", Src: "manual"})
	check("correction of the place replaces all of it", protocol.AgentStatus{GeoIP: lookup, Location: "广东 深圳", GeoOverride: &protocol.Geo{CC: "JP", Country: "日本", City: "东京"}},
		&protocol.Geo{CC: "JP", Country: "日本", City: "东京", Line: "电信", Src: "manual"})
}

// TestGeoColumnsAndOverrideAPI: the geo columns round-trip, an old database
// gains them, and PATCH /api/agents/{id} validates and applies a correction.
func TestGeoColumnsAndOverrideAPI(t *testing.T) {
	e := newTokenEnv(t)
	if code := e.call("POST", "/api/agents", `{"name":"n1","location":"广东 深圳","isp":"电信"}`, nil); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if err := e.st.SetAgentGeo("n1", "中国 浙江省 杭州市", "移动", parseRegion("中国|浙江省|杭州市|移动|CN")); err != nil {
		t.Fatal(err)
	}
	got, _ := e.st.GetAgent("n1")
	if got.GeoIP == nil || got.GeoIP.Province != "浙江" || got.GeoOverride != nil {
		t.Fatalf("stored lookup: %+v", got.GeoIP)
	}
	// The label wins over the lookup.
	list := func() protocol.AgentStatus {
		var l struct {
			Agents []protocol.AgentStatus `json:"agents"`
		}
		e.call("GET", "/api/agents", "", &l)
		if len(l.Agents) != 1 {
			t.Fatalf("agents: %+v", l.Agents)
		}
		return l.Agents[0]
	}
	if g := list().Geo; g == nil || g.Province != "广东" || g.City != "深圳" || g.Line != "电信" || g.Src != "label" {
		t.Fatalf("effective geo: %+v", g)
	}

	for body, want := range map[string]int{
		`{"geo":{"cc":"CN","province":"火星"}}`: http.StatusBadRequest,
		`{"geo":{"province":"广东"}}`:           http.StatusBadRequest,
		`{"geo":{"cc":"CHN"}}`:                http.StatusBadRequest,
		`{"geo":{"line":"卫星"}}`:               http.StatusBadRequest,
		`{"geo":{"cc":"hk","line":"IDC"}}`:    http.StatusOK,
		`{"geo":{"cc":"CN","province":"江苏省","city":"苏州市","line":"联通"}}`: http.StatusOK,
	} {
		if code := e.call("PATCH", "/api/agents/n1", body, nil); code != want {
			t.Errorf("PATCH %s: %d, want %d", body, code, want)
		}
	}
	if code := e.call("PATCH", "/api/agents/nope", `{"geo":{"line":"IDC"}}`, nil); code != http.StatusNotFound {
		t.Errorf("PATCH unknown node: %d", code)
	}
	e.call("PATCH", "/api/agents/n1", `{"geo":{"cc":"CN","province":"江苏省","city":"苏州市","line":"联通"}}`, nil)
	a := list()
	if g := a.Geo; g == nil || g.Province != "江苏" || g.City != "苏州" || g.Line != "联通" || g.Src != "manual" || a.GeoOverride == nil {
		t.Fatalf("after correction: %+v override %+v", g, a.GeoOverride)
	}
	// Clearing it brings the label back.
	if code := e.call("PATCH", "/api/agents/n1", `{"geo":null}`, nil); code != 200 {
		t.Fatalf("clear: %d", code)
	}
	if a := list(); a.Geo.Province != "广东" || a.GeoOverride != nil {
		t.Fatalf("after clearing: %+v", a.Geo)
	}
	// Guests may not correct anything.
	req, _ := http.NewRequest("PATCH", e.srv.URL+"/api/agents/n1", nil)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("guest PATCH: %v %v", err, resp)
	}

	// A result remembers its node's geography; a row from before the column
	// existed gets it from the display text.
	task := &protocol.Task{ID: "t1", Type: protocol.TaskPing, Target: "x"}
	if err := e.st.InsertTask(task); err != nil {
		t.Fatal(err)
	}
	_ = e.st.UpsertResult(&protocol.AgentResult{TaskID: "t1", AgentID: "a", Status: protocol.StatusDone, Geo: &protocol.Geo{CC: "JP", Country: "日本", Line: "IDC"}})
	_ = e.st.UpsertResult(&protocol.AgentResult{TaskID: "t1", AgentID: "b", Status: protocol.StatusDone, Location: "中国 广东省 深圳市", ISP: "电信"})
	rs, err := e.st.ListResults("t1")
	if err != nil || len(rs) != 2 {
		t.Fatalf("results: %v %d", err, len(rs))
	}
	for _, r := range rs {
		switch {
		case r.AgentID == "a" && (r.Geo == nil || r.Geo.CC != "JP" || r.Geo.Line != "IDC"):
			t.Errorf("stored result geo: %+v", r.Geo)
		case r.AgentID == "b" && (r.Geo == nil || r.Geo.Province != "广东" || r.Geo.Line != "电信"):
			t.Errorf("derived result geo: %+v", r.Geo)
		}
	}
}

func TestMigrateGeoColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	// Put the database back to how it looked before the geo columns.
	for _, q := range []string{`ALTER TABLE agents DROP COLUMN geo`, `ALTER TABLE agents DROP COLUMN geo_override`, `ALTER TABLE results DROP COLUMN geo`,
		`INSERT INTO agents (id,name,geo_location,geo_isp,first_seen,last_seen,auth) VALUES ('old','old','中国 四川省 成都市','联通',1,1,'legacy')`} {
		if _, err := st.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	st.Close()
	st, err = OpenStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st.Close()
	a, err := st.GetAgent("old")
	if err != nil || a.GeoIP != nil {
		t.Fatalf("old row: %v %+v", err, a)
	}
	if g := agentGeo(a); g == nil || g.Province != "四川" || g.City != "成都" || g.Line != "联通" {
		t.Fatalf("old row's geography from its labels: %+v", g)
	}
}

func TestStatsAPI(t *testing.T) {
	invalidateStats()
	t.Cleanup(invalidateStats)
	e := newTokenEnv(t)
	var tokens []string
	for _, body := range []string{`{"name":"sz","location":"广东 深圳"}`, `{"name":"hk","location":"香港"}`, `{"name":"lan","location":"本机"}`, `{"name":"never"}`} {
		var c struct {
			Token string `json:"token"`
		}
		e.call("POST", "/api/agents", body, &c)
		tokens = append(tokens, c.Token)
	}
	for i, name := range []string{"sz", "hk", "lan"} {
		conn, _, code, reason := e.dial(tokens[i], protocol.Hello{Name: name})
		if conn == nil {
			t.Fatalf("dial %s: %d %s", name, code, reason)
		}
		defer conn.Close()
	}
	now := e.st
	_ = now.InsertTask(&protocol.Task{ID: "s1", Type: protocol.TaskPing, Target: "x", CreatedAt: time.Now()})
	_ = now.InsertTask(&protocol.Task{ID: "s2", Type: protocol.TaskPing, Target: "x", MonitorID: "m", CreatedAt: time.Now()})
	_ = now.InsertTask(&protocol.Task{ID: "s0", Type: protocol.TaskPing, Target: "x", CreatedAt: time.Now().Add(-25 * time.Hour)})
	for i, st := range []protocol.ResultStatus{protocol.StatusDone, protocol.StatusDone, protocol.StatusDone, protocol.StatusError, protocol.StatusRunning} {
		_ = now.UpsertResult(&protocol.AgentResult{TaskID: "s1", AgentID: string(rune('a' + i)), Status: st})
	}

	// Guests see the same summary.
	resp, err := http.Get(e.srv.URL + "/api/stats")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("guest stats: %v %v", err, resp)
	}
	var st Stats
	_ = json.NewDecoder(resp.Body).Decode(&st)
	resp.Body.Close()
	n := st.Nodes
	if n.Total != 3 || n.Online != 3 || n.Mainland != 1 || n.Overseas != 1 || n.Unknown != 1 || n.Regions != 2 {
		t.Errorf("nodes: %+v", n)
	}
	if st.Tasks24h != 1 || st.MonitorRuns24h != 1 || st.SuccessRate24h == nil || *st.SuccessRate24h != 0.75 {
		t.Errorf("activity: %+v rate %v", st, st.SuccessRate24h)
	}
}

// TestAnnotateTarget: a result carries the address its node probed even
// without an IP database (the location is then simply absent).
func TestAnnotateTarget(t *testing.T) {
	var g *GeoIP
	for _, c := range []struct {
		typ  protocol.TaskType
		data string
		want string
	}{
		{protocol.TaskPing, `{"ip":"223.5.5.5","replies":[]}`, "223.5.5.5"},
		{protocol.TaskMTR, `{"ip":"2400:3200::1","hops":[]}`, "2400:3200::1"},
		{protocol.TaskHTTP, `{"attempts":[{"ip":"1.1.1.1"},{"ip":""},{"ip":"1.0.0.1"}]}`, "1.0.0.1"},
		{protocol.TaskDNS, `{"attempts":[{"server":"223.5.5.5:53"}]}`, ""},
		{protocol.TaskPing, ``, ""},
		{protocol.TaskPing, `not json`, ""},
	} {
		r := &protocol.AgentResult{Data: json.RawMessage(c.data)}
		g.AnnotateTarget(c.typ, r)
		if r.TargetIP != c.want || r.TargetGeo != "" {
			t.Errorf("%s %s: target %q geo %q, want %q", c.typ, c.data, r.TargetIP, r.TargetGeo, c.want)
		}
	}
}

// TestCountriesMirrored: the country picker in web/src/geo.js offers exactly
// the countries this package knows, under the same names, so a correction
// comes back named the way it was picked.
func TestCountriesMirrored(t *testing.T) {
	src, err := os.ReadFile("../../web/src/geo.js")
	if err != nil {
		t.Skip("web sources not found:", err)
	}
	js := string(src)
	start, end := strings.Index(js, "export const COMMON_COUNTRIES"), strings.Index(js, "export const COUNTRIES")
	if start < 0 || end < start {
		t.Fatal("COMMON_COUNTRIES / COUNTRIES not found in geo.js")
	}
	pairs := regexp.MustCompile(`\['([A-Z]{2})', '([^']+)'\]`).FindAllStringSubmatch(js[start:end], -1)
	seen := map[string]bool{}
	for _, p := range pairs {
		c, ok := countryByCode(p[1])
		switch {
		case !ok:
			t.Errorf("geo.js offers %s %s, unknown here", p[1], p[2])
		case c.zh != p[2]:
			t.Errorf("%s is %q here and %q in geo.js", p[1], c.zh, p[2])
		case seen[p[1]]:
			t.Errorf("%s listed twice in geo.js", p[1])
		}
		seen[p[1]] = true
	}
	for _, c := range countries {
		if !seen[c.cc] {
			t.Errorf("%s %s is missing from geo.js", c.cc, c.zh)
		}
	}
	if !seen["EE"] {
		t.Error("Estonia missing")
	}
}
