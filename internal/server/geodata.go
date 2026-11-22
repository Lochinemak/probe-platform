package server

import (
	"strings"
	"unicode"

	"probe-platform/internal/protocol"
)

// Structured node geography: where a node is (country / province / city) and
// which kind of line it sits on. The dashboard filters nodes by these and
// colours its maps with them, so they have to be the same few values however
// the information arrived: from the IP database, from the label an operator
// typed, or from an administrator's correction.

// Line categories. A node's ISP label is free text ("电信", "China Telecom",
// "腾讯云"); the dashboard groups by one of these instead.
const (
	LineTelecom   = "电信"
	LineUnicom    = "联通"
	LineMobile    = "移动"
	LineBroadcast = "广电"
	LineEducation = "教育网"
	LineIDC       = "IDC"
	LineCloud     = "云厂商"
	LineOther     = "其他"
)

var lineNames = []string{LineTelecom, LineUnicom, LineMobile, LineBroadcast, LineEducation, LineIDC, LineCloud, LineOther}

func validLine(s string) bool {
	for _, l := range lineNames {
		if s == l {
			return true
		}
	}
	return false
}

// lineOf sorts an ISP label into a line category, "" when there is nothing to
// go on. cc is the node's country code: the three Chinese carriers' English
// names ("telecom", "mobile") are ordinary words in other countries' ISP
// names, so they only count for a node known to be in China.
func lineOf(isp, cc string) string {
	s := strings.ToLower(strings.TrimSpace(isp))
	if s == "" || s == "0" {
		return ""
	}
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(s, w) {
				return true
			}
		}
		return false
	}
	// Chinese labels are unambiguous wherever they appear, bar one: 中华电信
	// is Taiwan's Chunghwa Telecom, not China Telecom.
	switch {
	case has("中华电信", "中華電信"):
		return LineOther
	case has("教育", "科研", "科技网"):
		return LineEducation
	case has("广电"):
		return LineBroadcast
	case has("电信"):
		return LineTelecom
	case has("联通", "网通"):
		return LineUnicom
	case has("移动", "铁通"):
		return LineMobile
	}
	if cc == "CN" {
		switch {
		case has("cernet", "education", "cstnet"):
			return LineEducation
		case has("broadcast", "cbn"):
			return LineBroadcast
		case has("chinanet", "telecom"):
			return LineTelecom
		case has("unicom", "cnc group", "china169"):
			return LineUnicom
		case has("mobile", "cmcc", "cmnet", "tietong"):
			return LineMobile
		}
	}
	switch {
	case has("云", "阿里", "腾讯", "华为", "百度", "火山", "金山", "京东", "cloud", "tencent", "alibaba", "aliyun", "huawei", "baidu", "amazon", "aws", "google", "microsoft", "azure", "oracle", "cloudflare", "volc", "bytedance", "ksyun", "kingsoft", "jdcloud", "qiniu"):
		return LineCloud
	case has("idc", "机房", "数据中心", "bgp", "多线", "datacenter", "data center", "hosting", "digitalocean", "vultr", "linode", "akamai", "hetzner", "ovh", "leaseweb", "equinix", "colo", "server", "vps"):
		return LineIDC
	}
	return LineOther
}

type provinceInfo struct {
	short, full string
	aliases     []string // other spellings, lower case
}

// provinces lists the 34 province-level divisions. Geo.Province always holds
// the short name; the dashboard's map joins on it.
var provinces = []provinceInfo{
	{"北京", "北京市", []string{"beijing"}}, {"天津", "天津市", []string{"tianjin"}},
	{"河北", "河北省", []string{"hebei"}}, {"山西", "山西省", []string{"shanxi"}},
	{"内蒙古", "内蒙古自治区", []string{"内蒙", "inner mongolia", "nei mongol"}},
	{"辽宁", "辽宁省", []string{"liaoning"}}, {"吉林", "吉林省", []string{"jilin"}},
	{"黑龙江", "黑龙江省", []string{"heilongjiang"}},
	{"上海", "上海市", []string{"shanghai"}}, {"江苏", "江苏省", []string{"jiangsu"}},
	{"浙江", "浙江省", []string{"zhejiang"}}, {"安徽", "安徽省", []string{"anhui"}},
	{"福建", "福建省", []string{"fujian"}}, {"江西", "江西省", []string{"jiangxi"}},
	{"山东", "山东省", []string{"shandong"}},
	{"河南", "河南省", []string{"henan"}}, {"湖北", "湖北省", []string{"hubei"}}, {"湖南", "湖南省", []string{"hunan"}},
	{"广东", "广东省", []string{"guangdong"}}, {"广西", "广西壮族自治区", []string{"guangxi"}},
	{"海南", "海南省", []string{"hainan"}},
	{"重庆", "重庆市", []string{"chongqing"}}, {"四川", "四川省", []string{"sichuan"}},
	{"贵州", "贵州省", []string{"guizhou"}}, {"云南", "云南省", []string{"yunnan"}},
	{"西藏", "西藏自治区", []string{"tibet", "xizang"}},
	{"陕西", "陕西省", []string{"shaanxi"}}, {"甘肃", "甘肃省", []string{"gansu"}},
	{"青海", "青海省", []string{"qinghai"}}, {"宁夏", "宁夏回族自治区", []string{"ningxia"}},
	{"新疆", "新疆维吾尔自治区", []string{"xinjiang"}},
	{"香港", "香港特别行政区", []string{"hong kong", "hongkong"}},
	{"澳门", "澳门特别行政区", []string{"macau", "macao", "澳門"}},
	{"台湾", "台湾省", []string{"taiwan", "台灣"}},
}

// outsideMainland are the province-level divisions the dashboard counts apart
// from the mainland.
var outsideMainland = map[string]bool{"香港": true, "澳门": true, "台湾": true}

// regionCodes maps the ISO codes some sources use for these three to the
// province they are filed under here.
var regionCodes = map[string]string{"HK": "香港", "MO": "澳门", "TW": "台湾"}

func findProvince(name string) (provinceInfo, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return provinceInfo{}, false
	}
	l := strings.ToLower(name)
	for _, p := range provinces {
		if name == p.short || name == p.full || strings.HasPrefix(name, p.short) {
			return p, true
		}
		for _, a := range p.aliases {
			if l == a {
				return p, true
			}
		}
	}
	return provinceInfo{}, false
}

// majorCities lets a label that names only a city ("深圳", "腾讯云 广州") land
// in the right province: capitals plus the cities people commonly have nodes in.
var majorCities = map[string]string{
	"石家庄": "河北", "唐山": "河北", "保定": "河北", "太原": "山西", "呼和浩特": "内蒙古", "包头": "内蒙古",
	"沈阳": "辽宁", "大连": "辽宁", "长春": "吉林", "哈尔滨": "黑龙江",
	"南京": "江苏", "苏州": "江苏", "无锡": "江苏", "常州": "江苏", "徐州": "江苏", "南通": "江苏", "扬州": "江苏",
	"杭州": "浙江", "宁波": "浙江", "温州": "浙江", "嘉兴": "浙江", "绍兴": "浙江", "金华": "浙江", "台州": "浙江",
	"合肥": "安徽", "芜湖": "安徽", "福州": "福建", "厦门": "福建", "泉州": "福建", "南昌": "江西",
	"济南": "山东", "青岛": "山东", "烟台": "山东", "潍坊": "山东",
	"郑州": "河南", "洛阳": "河南", "武汉": "湖北", "宜昌": "湖北", "襄阳": "湖北", "长沙": "湖南",
	"广州": "广东", "深圳": "广东", "东莞": "广东", "佛山": "广东", "珠海": "广东", "中山": "广东", "惠州": "广东", "汕头": "广东",
	"南宁": "广西", "桂林": "广西", "柳州": "广西", "海口": "海南", "三亚": "海南",
	"成都": "四川", "绵阳": "四川", "贵阳": "贵州", "昆明": "云南", "拉萨": "西藏",
	"西安": "陕西", "兰州": "甘肃", "西宁": "青海", "银川": "宁夏", "乌鲁木齐": "新疆",
}

type countryInfo struct {
	cc, zh string
	en     []string // lower case
}

// countries covers where nodes realistically are; anything else keeps the name
// its source gave it.
var countries = []countryInfo{
	{"CN", "中国", []string{"china"}},
	{"US", "美国", []string{"united states", "usa", "america"}}, {"JP", "日本", []string{"japan"}},
	{"KR", "韩国", []string{"south korea", "korea"}}, {"SG", "新加坡", []string{"singapore"}},
	{"DE", "德国", []string{"germany"}}, {"GB", "英国", []string{"united kingdom", "britain", "england"}},
	{"FR", "法国", []string{"france"}}, {"NL", "荷兰", []string{"netherlands", "holland"}},
	{"RU", "俄罗斯", []string{"russia"}}, {"CA", "加拿大", []string{"canada"}},
	{"AU", "澳大利亚", []string{"australia"}}, {"NZ", "新西兰", []string{"new zealand"}},
	{"IN", "印度", []string{"india"}}, {"ID", "印度尼西亚", []string{"indonesia"}},
	{"MY", "马来西亚", []string{"malaysia"}}, {"TH", "泰国", []string{"thailand"}},
	{"VN", "越南", []string{"vietnam", "viet nam"}}, {"PH", "菲律宾", []string{"philippines"}},
	{"KH", "柬埔寨", []string{"cambodia"}}, {"MM", "缅甸", []string{"myanmar"}}, {"LA", "老挝", []string{"laos"}},
	{"MN", "蒙古", []string{"mongolia"}}, {"KZ", "哈萨克斯坦", []string{"kazakhstan"}},
	{"PK", "巴基斯坦", []string{"pakistan"}}, {"BD", "孟加拉国", []string{"bangladesh"}},
	{"AE", "阿联酋", []string{"united arab emirates", "uae"}}, {"SA", "沙特阿拉伯", []string{"saudi arabia"}},
	{"TR", "土耳其", []string{"turkey", "türkiye"}}, {"IL", "以色列", []string{"israel"}},
	{"IT", "意大利", []string{"italy"}}, {"ES", "西班牙", []string{"spain"}}, {"PT", "葡萄牙", []string{"portugal"}},
	{"SE", "瑞典", []string{"sweden"}}, {"NO", "挪威", []string{"norway"}}, {"FI", "芬兰", []string{"finland"}},
	{"DK", "丹麦", []string{"denmark"}}, {"CH", "瑞士", []string{"switzerland"}}, {"AT", "奥地利", []string{"austria"}},
	{"BE", "比利时", []string{"belgium"}}, {"IE", "爱尔兰", []string{"ireland"}}, {"PL", "波兰", []string{"poland"}},
	{"CZ", "捷克", []string{"czechia", "czech republic"}}, {"HU", "匈牙利", []string{"hungary"}},
	{"RO", "罗马尼亚", []string{"romania"}}, {"BG", "保加利亚", []string{"bulgaria"}}, {"GR", "希腊", []string{"greece"}},
	{"UA", "乌克兰", []string{"ukraine"}}, {"LU", "卢森堡", []string{"luxembourg"}}, {"IS", "冰岛", []string{"iceland"}},
	{"BR", "巴西", []string{"brazil"}}, {"MX", "墨西哥", []string{"mexico"}}, {"AR", "阿根廷", []string{"argentina"}},
	{"CL", "智利", []string{"chile"}}, {"CO", "哥伦比亚", []string{"colombia"}},
	{"ZA", "南非", []string{"south africa"}}, {"EG", "埃及", []string{"egypt"}}, {"NG", "尼日利亚", []string{"nigeria"}},
}

func countryByCode(cc string) (countryInfo, bool) {
	for _, c := range countries {
		if c.cc == cc {
			return c, true
		}
	}
	return countryInfo{}, false
}

func countryByName(name string) (countryInfo, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return countryInfo{}, false
	}
	l := strings.ToLower(name)
	for _, c := range countries {
		if name == c.zh {
			return c, true
		}
		for _, e := range c.en {
			if l == e {
				return c, true
			}
		}
	}
	return countryInfo{}, false
}

func trimCity(s string) string {
	s = strings.TrimSpace(s)
	if s == "0" {
		return ""
	}
	if r := []rune(s); len(r) > 2 && r[len(r)-1] == '市' {
		return string(r[:len(r)-1])
	}
	return s
}

// chinaGeo builds the Geo of a place in China from whatever spelling of the
// province and city the source used.
func chinaGeo(province, city string) protocol.Geo {
	g := protocol.Geo{CC: "CN", Country: "中国"}
	if p, ok := findProvince(province); ok {
		g.Province = p.short
	}
	g.City = trimCity(city)
	return g
}

// newGeo normalises one looked-up place: Hong Kong, Macau and Taiwan are
// filed as provinces whichever way the source codes them, Chinese provinces
// get their short names, and other countries their Chinese names.
func newGeo(cc, country, province, city string) protocol.Geo {
	cc = strings.ToUpper(strings.TrimSpace(cc))
	clean := func(s string) string {
		if s = strings.TrimSpace(s); s == "0" {
			return ""
		}
		return s
	}
	country, province, city = clean(country), clean(province), clean(city)
	if p, ok := regionCodes[cc]; ok {
		if city == "" && province != "" && !strings.HasPrefix(province, p) {
			city = province
		}
		return chinaGeo(p, city)
	}
	if cc == "" {
		if c, ok := countryByName(country); ok {
			cc = c.cc
		}
	}
	if cc == "CN" {
		return chinaGeo(province, city)
	}
	g := protocol.Geo{CC: cc, Country: country, Province: province, City: city}
	if c, ok := countryByCode(cc); ok {
		g.Country = c.zh
	}
	if g.Province == g.Country {
		g.Province = ""
	}
	if g.City == g.Province || g.City == g.Country {
		g.City = ""
	}
	return g
}

// parseRegion turns an ip2region record into a place and a line. Both layouts
// are understood: v3 `国家|省份|城市|ISP|国家代码` and v2 `国家|区域|省份|城市|ISP`.
func parseRegion(raw string) protocol.Geo {
	parts := strings.Split(raw, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	get := func(i int) string {
		if i < len(parts) {
			return parts[i]
		}
		return ""
	}
	var g protocol.Geo
	var isp string
	switch {
	case len(parts) >= 5 && isCountryCode(parts[len(parts)-1]):
		g = newGeo(parts[len(parts)-1], get(0), get(1), get(2))
		isp = get(3)
	case len(parts) >= 5:
		g = newGeo("", get(0), get(2), get(3))
		isp = get(4)
	default:
		g = newGeo("", get(0), get(1), get(2))
		isp = get(3)
	}
	if g.CC == "" && g.Country == "" {
		return protocol.Geo{}
	}
	g.Line = lineOf(isp, g.CC)
	return g
}

func labelFields(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(",，、/|·-_()（）[]", r)
	})
}

// parseLabel reads a place out of free text: the location an operator typed
// for a node ("广东 深圳", "腾讯云广州", "日本 东京", "US-LA") or a location
// string stored by an older version ("中国 广东省 深圳市"). ok is false when
// no province or country can be recognised.
func parseLabel(s string) (protocol.Geo, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return protocol.Geo{}, false
	}
	lower := strings.ToLower(s)
	fields := labelFields(s)

	// A Chinese province, by any of its names.
	for _, p := range provinces {
		hit := strings.Contains(s, p.short)
		for _, a := range p.aliases {
			hit = hit || containsWord(lower, a)
		}
		if !hit {
			continue
		}
		rest := s
		for _, w := range []string{"中国", p.full, p.short + "省", p.short + "市", p.short} {
			rest = strings.ReplaceAll(rest, w, " ")
		}
		city := ""
		for _, f := range labelFields(rest) {
			// "广东电信": what is left is the carrier, not a city.
			if l := lineOf(f, "CN"); isHan(f) && (l == "" || l == LineOther) {
				city = trimCity(f)
				break
			}
		}
		g := chinaGeo(p.short, city)
		if g.City == "" {
			for c, prov := range majorCities {
				if prov == p.short && strings.Contains(s, c) {
					g.City = c
				}
			}
		}
		return g, true
	}
	// A well-known city on its own.
	for c, prov := range majorCities {
		if strings.Contains(s, c) {
			return chinaGeo(prov, c), true
		}
	}
	// A country: its Chinese name, its English name, or a two-letter code
	// standing alone ("US-LA").
	for _, f := range fields {
		if p, ok := regionCodes[f]; ok {
			return chinaGeo(p, ""), true
		}
	}
	for _, c := range countries {
		hit := strings.Contains(s, c.zh)
		at := c.zh
		for _, e := range c.en {
			if !hit && containsWord(lower, e) {
				hit, at = true, e
			}
		}
		for _, f := range fields {
			if !hit && f == c.cc {
				hit, at = true, f
			}
		}
		if !hit {
			continue
		}
		g := protocol.Geo{CC: c.cc, Country: c.zh}
		if c.cc == "CN" {
			return g, true
		}
		// What follows the country is the place within it.
		rest := s
		if i := strings.Index(lower, strings.ToLower(at)); i >= 0 {
			rest = s[i+len(at):]
		}
		switch f := labelFields(rest); {
		case len(f) >= 2 && isHan(f[0]):
			g.Province, g.City = f[0], trimCity(f[1])
		case len(f) >= 1 && isHan(f[0]):
			g.City = trimCity(f[0])
		default:
			g.City = strings.TrimSpace(strings.Trim(rest, " ,-_/|·"))
		}
		return g, true
	}
	return protocol.Geo{}, false
}

// containsWord reports whether lower contains word with no letter on either
// side, so "us" does not match inside "russia".
func containsWord(lower, word string) bool {
	for from := 0; ; {
		i := strings.Index(lower[from:], word)
		if i < 0 {
			return false
		}
		i += from
		before, after := rune(' '), rune(' ')
		if i > 0 {
			before = rune(lower[i-1])
		}
		if j := i + len(word); j < len(lower) {
			after = rune(lower[j])
		}
		if !isASCIILetter(before) && !isASCIILetter(after) {
			return true
		}
		from = i + len(word)
	}
}

func isASCIILetter(r rune) bool { return r >= 'a' && r <= 'z' }

func isHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// agentGeo works out where a node is from everything known about it. Later
// sources override earlier ones field by field:
//
//  1. the IP database's answer for its public address (AgentStatus.GeoIP, or
//     for records written before that existed, the GeoLocation / GeoISP text);
//  2. the location / ISP labels the operator gave the node, as far as they can
//     be read: a recognised place replaces the looked-up one, a recognised
//     line replaces the looked-up line;
//  3. the administrator's correction (GeoOverride).
//
// It returns nil when nothing is known.
func agentGeo(a *protocol.AgentStatus) *protocol.Geo {
	var g protocol.Geo
	src := ""
	if a.GeoIP != nil {
		g, src = *a.GeoIP, "geoip"
	} else {
		if pg, ok := parseLabel(a.GeoLocation); ok {
			g, src = pg, "geoip"
		}
		if l := lineOf(a.GeoISP, g.CC); l != "" {
			g.Line, src = l, "geoip"
		}
	}
	if lg, ok := parseLabel(a.Location); ok {
		if lg.City == "" && lg.CC == g.CC && lg.Province == g.Province {
			lg.City = g.City // the label names the province, the lookup knows the city
		}
		lg.Line = g.Line
		g, src = lg, "label"
	}
	if l := lineOf(a.ISP, g.CC); l != "" && (l != LineOther || g.Line == "") {
		g.Line, src = l, "label"
	}
	if o := a.GeoOverride; o != nil {
		if o.CC != "" {
			g.CC, g.Country, g.Province, g.City = o.CC, o.Country, o.Province, o.City
		}
		if o.Line != "" {
			g.Line = o.Line
		}
		src = "manual"
	}
	if g == (protocol.Geo{}) {
		return nil
	}
	g.Src = src
	return &g
}

// labelGeo is agentGeo for a result row stored before results recorded their
// node's geography: all that is left is the display text.
func labelGeo(location, isp string) *protocol.Geo {
	return agentGeo(&protocol.AgentStatus{GeoLocation: location, GeoISP: isp})
}

// isMainland reports whether g is in mainland China.
func isMainland(g *protocol.Geo) bool {
	return g != nil && g.CC == "CN" && !outsideMainland[g.Province]
}
