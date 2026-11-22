package server

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"probe-platform/internal/probe"
	"probe-platform/internal/protocol"
)

// IPInfo is what the IP lookup page shows for one address.
type IPInfo struct {
	IP      string `json:"ip"`
	Version int    `json:"version"` // 4 or 6
	// Kind: "public", "private" (LAN, loopback, CGNAT …) or "fake_ip"
	// (198.18.0.0/15, handed out by transparent proxies).
	Kind     string        `json:"kind"`
	Location string        `json:"location,omitempty"` // as the database words it
	ISP      string        `json:"isp,omitempty"`
	Geo      *protocol.Geo `json:"geo,omitempty"`
	// The rest is only known from the online lookup.
	ASN     string   `json:"asn,omitempty"`      // "AS4134 Chinanet"
	NetType string   `json:"net_type,omitempty"` // 移动网络 / 机房或托管
	Lat     *float64 `json:"lat,omitempty"`
	Lon     *float64 `json:"lon,omitempty"`
	// Source: "offline" (the local ip2region database), "online" (ip-api.com)
	// or "" when nothing is known.
	Source string `json:"source,omitempty"`
}

// LookupInfo describes ip. The local database is asked first; with online
// set and online lookups enabled, ip-api.com fills in what the database does
// not have (ASN, network type, coordinates) or does not know at all. online
// is only set for an address someone typed in: the free ip-api endpoint is
// plain HTTP, and a visitor's own address is not sent anywhere unasked.
func (g *GeoIP) LookupInfo(ipStr string, online bool) IPInfo {
	ip := net.ParseIP(ipStr)
	info := IPInfo{IP: ipStr, Version: 6, Kind: "public"}
	if ip == nil {
		return info
	}
	info.IP = ip.String()
	if ip.To4() != nil {
		info.Version = 4
	}
	switch {
	case probe.IsFakeIP(ip):
		info.Kind = "fake_ip"
		return info
	case !probe.IsPublicIP(ip):
		info.Kind = "private"
		return info
	}
	if g == nil {
		return info
	}
	if raw := g.offlineRaw(ip); raw != "" {
		info.Location, info.ISP = splitRegion(raw)
		if geo := parseRegion(raw); geo != (protocol.Geo{}) {
			info.Geo = &geo
		}
		if info.Location != "" || info.ISP != "" {
			info.Source = "offline"
		}
	}
	if online && g.online {
		g.fillOnline(&info)
	}
	return info
}

// fillOnline asks ip-api.com about info.IP and merges the answer in: the
// offline place and ISP stay when there are any, the rest is added.
func (g *GeoIP) fillOnline(info *IPInfo) {
	g.mu.Lock()
	cached, ok := g.infos[info.IP]
	g.mu.Unlock()
	if !ok || time.Since(cached.at) > g.agentTTL {
		u := fmt.Sprintf("http://ip-api.com/json/%s?lang=zh-CN&fields=status,message,country,countryCode,regionName,city,isp,org,as,mobile,hosting,lat,lon", url.PathEscape(info.IP))
		resp, err := g.http.Get(u)
		if err != nil {
			g.log.Debug("ip-api lookup failed", "err", err)
			return
		}
		defer resp.Body.Close()
		var body ipAPIBody
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.Status != "success" {
			g.log.Debug("ip-api lookup rejected", "status", body.Status, "msg", body.Message)
			return
		}
		cached = ipAPIEntry{body: body, at: time.Now()}
		g.mu.Lock()
		if len(g.infos) > 5000 {
			g.infos = map[string]ipAPIEntry{}
		}
		g.infos[info.IP] = cached
		g.mu.Unlock()
	}
	b := cached.body
	if info.Geo == nil {
		geo := newGeo(b.CountryCode, b.Country, b.RegionName, b.City)
		geo.Line = lineOf(firstNonEmpty(b.ISP, b.Org), geo.CC)
		if geo != (protocol.Geo{}) {
			info.Geo = &geo
		}
		var parts []string
		for _, p := range []string{b.Country, b.RegionName, b.City} {
			if p = strings.TrimSpace(p); p != "" && (len(parts) == 0 || parts[len(parts)-1] != p) {
				parts = append(parts, p)
			}
		}
		info.Location = strings.Join(parts, " ")
	}
	if info.ISP == "" {
		info.ISP = firstNonEmpty(normalizeISP(b.ISP), normalizeISP(b.Org))
	}
	info.ASN = b.AS
	switch {
	case b.Mobile:
		info.NetType = "移动网络"
	case b.Hosting:
		info.NetType = "机房 / 托管"
	}
	if b.Lat != 0 || b.Lon != 0 {
		lat, lon := b.Lat, b.Lon
		info.Lat, info.Lon = &lat, &lon
	}
	if info.Source == "" {
		info.Source = "online"
	} else {
		info.Source = "offline+online"
	}
}

type ipAPIBody struct {
	Status, Message, Country, CountryCode, RegionName, City, ISP, Org, AS string
	Mobile, Hosting                                                       bool
	Lat, Lon                                                              float64
}

type ipAPIEntry struct {
	body ipAPIBody
	at   time.Time
}

// Limits on the lookup endpoints: per visitor, and for everybody together on
// the calls that go out to ip-api.com (its free tier allows 45 a minute).
var (
	ipLookupLimiter = newRateLimiter(20, time.Minute)
	ipOnlineLimiter = newRateLimiter(30, time.Minute)
)

// ipMe reports the address this request came from, as the server sees it.
func (a *API) ipMe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, a.hub.geo.LookupInfo(a.cfg.ClientIP(r), false))
}

func (a *API) ipLookup(w http.ResponseWriter, r *http.Request) {
	q := strings.Trim(strings.TrimSpace(r.URL.Query().Get("ip")), "[]")
	ip := net.ParseIP(q)
	if ip == nil {
		writeErr(w, http.StatusBadRequest, "请输入有效的 IPv4 或 IPv6 地址")
		return
	}
	if !a.auth.isAdmin(r) && !ipLookupLimiter.allow(a.cfg.ClientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, "查询过于频繁，请稍后再试")
		return
	}
	online := probe.IsPublicIP(ip) && ipOnlineLimiter.allow("all")
	writeJSON(w, http.StatusOK, a.hub.geo.LookupInfo(ip.String(), online))
}
