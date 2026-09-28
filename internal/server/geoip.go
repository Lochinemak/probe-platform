package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lionsoul2014/ip2region/binding/golang/xdb"

	"probe-platform/internal/protocol"
)

// GeoIP annotates IP addresses with a location / ISP label.
//
//   - Agent public IPs are looked up online via ip-api.com (free, 45 req/min,
//     cached for a day) when enabled, falling back to the offline database.
//   - MTR hops are annotated offline only, using ip2region xdb files
//     (ip2region.xdb for IPv4 and optionally ip2region_v6.xdb) because a
//     single MTR easily produces dozens of lookups.
type GeoIP struct {
	online bool
	http   *http.Client
	log    *slog.Logger

	mu       sync.Mutex
	v4, v6   *xdb.Searcher
	agentTTL time.Duration
	agents   map[string]geoEntry
	hops     map[string]string
}

type geoEntry struct {
	loc, isp string
	at       time.Time
}

// NewGeoIP returns nil when neither online lookup nor an offline database is
// available.
func NewGeoIP(cfg Config, log *slog.Logger) *GeoIP {
	g := &GeoIP{
		online:   cfg.GeoIPOnline,
		http:     &http.Client{Timeout: 6 * time.Second},
		log:      log,
		agentTTL: 24 * time.Hour,
		agents:   map[string]geoEntry{},
		hops:     map[string]string{},
	}
	candidates := []string{}
	if cfg.IP2RegionDB != "" {
		candidates = append(candidates, cfg.IP2RegionDB, filepath.Join(filepath.Dir(cfg.IP2RegionDB), "ip2region_v6.xdb"))
	}
	for _, name := range []string{"ip2region.xdb", "ip2region_v4.xdb", "ip2region_v6.xdb"} {
		candidates = append(candidates, filepath.Join(cfg.DataDir, name))
	}
	seen := map[string]bool{}
	for _, p := range candidates {
		if seen[p] {
			continue
		}
		seen[p] = true
		if _, err := os.Stat(p); err != nil {
			continue
		}
		s, ver, err := loadXDB(p)
		if err != nil {
			log.Warn("ip2region: failed to load", "path", p, "err", err)
			continue
		}
		switch ver.Id {
		case xdb.IPv4VersionNo:
			if g.v4 == nil {
				g.v4 = s
				log.Info("ip2region: IPv4 database loaded", "path", p)
			}
		case xdb.IPv6VersionNo:
			if g.v6 == nil {
				g.v6 = s
				log.Info("ip2region: IPv6 database loaded", "path", p)
			}
		}
	}
	if g.v4 == nil {
		log.Info("ip2region: no xdb found, MTR hops will not be annotated (drop ip2region.xdb into the data dir)")
	}
	if !g.online && g.v4 == nil && g.v6 == nil {
		return nil
	}
	return g
}

func loadXDB(path string) (*xdb.Searcher, *xdb.Version, error) {
	buf, err := xdb.LoadContentFromFile(path)
	if err != nil {
		return nil, nil, err
	}
	hdr, err := xdb.LoadHeaderFromBuff(buf)
	if err != nil {
		return nil, nil, err
	}
	ver, err := xdb.VersionFromHeader(hdr)
	if err != nil {
		return nil, nil, err
	}
	s, err := xdb.NewWithBuffer(ver, buf)
	if err != nil {
		return nil, nil, err
	}
	return s, ver, nil
}

// HasOffline reports whether an offline database is loaded.
func (g *GeoIP) HasOffline() bool { return g != nil && (g.v4 != nil || g.v6 != nil) }

func isPrivateIP(ip net.IP) bool {
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() ||
		(ip.To4() != nil && ip.To4()[0] == 100 && ip.To4()[1] >= 64 && ip.To4()[1] <= 127) // CGNAT 100.64/10
}

// offlineRaw returns the raw ip2region region string ("中国|广东省|深圳市|电信").
func (g *GeoIP) offlineRaw(ip net.IP) string {
	var s *xdb.Searcher
	if ip.To4() != nil {
		s = g.v4
	} else {
		s = g.v6
	}
	if s == nil {
		return ""
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	region, err := s.Search(ip.String())
	if err != nil {
		return ""
	}
	return region
}

// splitRegion turns an ip2region region string into ("中国 广东省 深圳市", "电信").
// Both the v2 layout (国家|区域|省份|城市|ISP) and the v3 layout
// (国家|省份|城市|ISP|国家代码) are handled; "0" means unknown.
func splitRegion(region string) (loc, isp string) {
	parts := strings.Split(region, "|")
	if len(parts) == 0 {
		return "", ""
	}
	// v3 appends an ISO country code; drop it.
	if last := strings.TrimSpace(parts[len(parts)-1]); len(parts) >= 4 && isCountryCode(last) {
		parts = parts[:len(parts)-1]
	}
	last := strings.TrimSpace(parts[len(parts)-1])
	if last != "0" && last != "" && len(parts) > 1 {
		isp = last
	}
	if len(parts) > 1 {
		parts = parts[:len(parts)-1]
	}
	var out []string
	prev := ""
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || p == "0" || p == prev {
			continue
		}
		out = append(out, p)
		prev = p
	}
	return strings.Join(out, " "), isp
}

func isCountryCode(s string) bool {
	if len(s) != 2 {
		return false
	}
	return s[0] >= 'A' && s[0] <= 'Z' && s[1] >= 'A' && s[1] <= 'Z'
}

// Offline returns a single display label for ip using the local database, or
// "" when unavailable.
func (g *GeoIP) Offline(ipStr string) string {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return ""
	}
	if isPrivateIP(ip) {
		return "内网"
	}
	g.mu.Lock()
	if v, ok := g.hops[ipStr]; ok {
		g.mu.Unlock()
		return v
	}
	g.mu.Unlock()
	raw := g.offlineRaw(ip)
	label := ""
	if raw != "" {
		loc, isp := splitRegion(raw)
		label = strings.TrimSpace(loc + " " + isp)
	}
	g.mu.Lock()
	if len(g.hops) > 50000 {
		g.hops = map[string]string{}
	}
	g.hops[ipStr] = label
	g.mu.Unlock()
	return label
}

// AnnotateHops fills MTRHop.Geo for every host when an offline db is loaded.
func (g *GeoIP) AnnotateHops(hops []protocol.MTRHop) {
	if g == nil || !g.HasOffline() {
		return
	}
	for i := range hops {
		hops[i].Geo = make([]string, len(hops[i].Hosts))
		for j, h := range hops[i].Hosts {
			hops[i].Geo[j] = g.Offline(h)
		}
	}
}

// LookupAgent resolves an agent's public IP to (location, isp).
func (g *GeoIP) LookupAgent(ipStr string) (loc, isp string) {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return "", ""
	}
	if isPrivateIP(ip) {
		return "内网", ""
	}
	g.mu.Lock()
	if e, ok := g.agents[ipStr]; ok && time.Since(e.at) < g.agentTTL {
		g.mu.Unlock()
		return e.loc, e.isp
	}
	g.mu.Unlock()

	// Offline first: the free ip-api.com endpoint is plain HTTP only (its TLS
	// endpoint needs a paid key), so every online lookup puts a node's public
	// IP on the wire in the clear and lets anyone on the path forge the answer.
	// With a local database most addresses never need the network call.
	if raw := g.offlineRaw(ip); raw != "" {
		loc, isp = splitRegion(raw)
	}
	if loc == "" && g.online {
		loc, isp = g.lookupIPAPI(ipStr)
	}
	if loc != "" || isp != "" {
		g.mu.Lock()
		g.agents[ipStr] = geoEntry{loc: loc, isp: isp, at: time.Now()}
		g.mu.Unlock()
	}
	return loc, isp
}

func (g *GeoIP) lookupIPAPI(ip string) (loc, isp string) {
	u := fmt.Sprintf("http://ip-api.com/json/%s?lang=zh-CN&fields=status,message,country,regionName,city,isp,org,as", url.PathEscape(ip))
	resp, err := g.http.Get(u)
	if err != nil {
		g.log.Debug("ip-api lookup failed", "ip", ip, "err", err)
		return "", ""
	}
	defer resp.Body.Close()
	var body struct {
		Status, Message, Country, RegionName, City, ISP, Org, AS string
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.Status != "success" {
		g.log.Debug("ip-api lookup rejected", "ip", ip, "status", body.Status, "msg", body.Message)
		return "", ""
	}
	var parts []string
	prev := ""
	for _, p := range []string{body.Country, body.RegionName, body.City} {
		p = strings.TrimSpace(p)
		if p != "" && p != prev {
			parts = append(parts, p)
			prev = p
		}
	}
	loc = strings.Join(parts, " ")
	isp = normalizeISP(body.ISP)
	if isp == "" {
		isp = normalizeISP(body.Org)
	}
	return loc, isp
}

var ispAliases = []struct{ match, label string }{
	{"chinanet", "电信"}, {"china telecom", "电信"}, {"telecom", "电信"},
	{"unicom", "联通"}, {"cnc group", "联通"}, {"china169", "联通"},
	{"china mobile", "移动"}, {"cmcc", "移动"}, {"cmnet", "移动"},
	{"cernet", "教育网"}, {"tietong", "铁通"}, {"broadcast", "广电"}, {"cbn", "广电"},
	{"tencent", "腾讯云"}, {"alibaba", "阿里云"}, {"aliyun", "阿里云"}, {"huawei", "华为云"},
	{"baidu", "百度云"}, {"ucloud", "UCloud"}, {"amazon", "AWS"}, {"google", "Google Cloud"},
	{"microsoft", "Azure"}, {"cloudflare", "Cloudflare"}, {"digitalocean", "DigitalOcean"},
	{"vultr", "Vultr"}, {"linode", "Linode"}, {"akamai", "Akamai"}, {"oracle", "Oracle Cloud"},
	{"hong kong broadband", "HKBN"}, {"hkt", "HKT"}, {"pccw", "PCCW"}, {"hinet", "HiNet"},
	{"chunghwa", "中華電信"}, {"so-net", "So-net"}, {"ntt", "NTT"}, {"kddi", "KDDI"}, {"softbank", "SoftBank"},
}

func normalizeISP(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	l := strings.ToLower(s)
	for _, a := range ispAliases {
		if strings.Contains(l, a.match) {
			return a.label
		}
	}
	return s
}
