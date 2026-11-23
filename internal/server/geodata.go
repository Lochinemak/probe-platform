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

// countries is every country and territory with an ISO 3166 code except
// China's three regions (filed as provinces, see regionCodes) and places with
// nobody to host a node. Chinese names are ICU's (zh-CN), English names are
// matched as whole words in node labels. web/src/geo.js COUNTRIES mirrors the
// codes and names (TestCountriesMirrored). Generated, then trimmed by hand:
// English names that are as often something else ("Georgia", "Jersey") are
// left out.
var countries = []countryInfo{
	{"CN", "中国", []string{"china"}},
	{"AD", "安道尔", []string{"andorra"}},
	{"AE", "阿联酋", []string{"united arab emirates", "uae"}},
	{"AF", "阿富汗", []string{"afghanistan"}},
	{"AG", "安提瓜和巴布达", []string{"antigua and barbuda"}},
	{"AI", "安圭拉", []string{"anguilla"}},
	{"AL", "阿尔巴尼亚", []string{"albania"}},
	{"AM", "亚美尼亚", []string{"armenia"}},
	{"AO", "安哥拉", []string{"angola"}},
	{"AR", "阿根廷", []string{"argentina"}},
	{"AS", "美属萨摩亚", []string{"american samoa"}},
	{"AT", "奥地利", []string{"austria"}},
	{"AU", "澳大利亚", []string{"australia"}},
	{"AW", "阿鲁巴", []string{"aruba"}},
	{"AX", "奥兰群岛", []string{}},
	{"AZ", "阿塞拜疆", []string{"azerbaijan"}},
	{"BA", "波斯尼亚和黑塞哥维那", []string{"bosnia and herzegovina", "bosnia"}},
	{"BB", "巴巴多斯", []string{"barbados"}},
	{"BD", "孟加拉国", []string{"bangladesh"}},
	{"BE", "比利时", []string{"belgium"}},
	{"BF", "布基纳法索", []string{"burkina faso"}},
	{"BG", "保加利亚", []string{"bulgaria"}},
	{"BH", "巴林", []string{"bahrain"}},
	{"BI", "布隆迪", []string{"burundi"}},
	{"BJ", "贝宁", []string{"benin"}},
	{"BL", "圣巴泰勒米", []string{}},
	{"BM", "百慕大", []string{"bermuda"}},
	{"BN", "文莱", []string{"brunei"}},
	{"BO", "玻利维亚", []string{"bolivia"}},
	{"BQ", "荷属加勒比区", []string{"caribbean netherlands"}},
	{"BR", "巴西", []string{"brazil"}},
	{"BS", "巴哈马", []string{"bahamas"}},
	{"BT", "不丹", []string{"bhutan"}},
	{"BW", "博茨瓦纳", []string{"botswana"}},
	{"BY", "白俄罗斯", []string{"belarus"}},
	{"BZ", "伯利兹", []string{"belize"}},
	{"CA", "加拿大", []string{"canada"}},
	{"CC", "科科斯群岛", []string{}},
	{"CD", "刚果（金）", []string{"democratic republic of the congo", "dr congo"}},
	{"CF", "中非共和国", []string{"central african republic"}},
	{"CG", "刚果（布）", []string{"republic of the congo"}},
	{"CH", "瑞士", []string{"switzerland"}},
	{"CI", "科特迪瓦", []string{"ivory coast"}},
	{"CK", "库克群岛", []string{"cook islands"}},
	{"CL", "智利", []string{"chile"}},
	{"CM", "喀麦隆", []string{"cameroon"}},
	{"CO", "哥伦比亚", []string{"colombia"}},
	{"CR", "哥斯达黎加", []string{"costa rica"}},
	{"CU", "古巴", []string{"cuba"}},
	{"CV", "佛得角", []string{"cape verde", "cabo verde"}},
	{"CW", "库拉索", []string{"curacao"}},
	{"CX", "圣诞岛", []string{"christmas island"}},
	{"CY", "塞浦路斯", []string{"cyprus"}},
	{"CZ", "捷克", []string{"czechia", "czech republic"}},
	{"DE", "德国", []string{"germany"}},
	{"DJ", "吉布提", []string{"djibouti"}},
	{"DK", "丹麦", []string{"denmark"}},
	{"DM", "多米尼克", []string{"dominica"}},
	{"DO", "多米尼加共和国", []string{"dominican republic"}},
	{"DZ", "阿尔及利亚", []string{"algeria"}},
	{"EC", "厄瓜多尔", []string{"ecuador"}},
	{"EE", "爱沙尼亚", []string{"estonia"}},
	{"EG", "埃及", []string{"egypt"}},
	{"EH", "西撒哈拉", []string{"western sahara"}},
	{"ER", "厄立特里亚", []string{"eritrea"}},
	{"ES", "西班牙", []string{"spain"}},
	{"ET", "埃塞俄比亚", []string{"ethiopia"}},
	{"FI", "芬兰", []string{"finland"}},
	{"FJ", "斐济", []string{"fiji"}},
	{"FK", "福克兰群岛", []string{"falkland islands"}},
	{"FM", "密克罗尼西亚", []string{"micronesia"}},
	{"FO", "法罗群岛", []string{"faroe islands"}},
	{"FR", "法国", []string{"france"}},
	{"GA", "加蓬", []string{"gabon"}},
	{"GB", "英国", []string{"united kingdom", "britain", "england", "uk"}},
	{"GD", "格林纳达", []string{"grenada"}},
	{"GE", "格鲁吉亚", []string{}},
	{"GF", "法属圭亚那", []string{"french guiana"}},
	{"GG", "根西岛", []string{"guernsey"}},
	{"GH", "加纳", []string{"ghana"}},
	{"GI", "直布罗陀", []string{"gibraltar"}},
	{"GL", "格陵兰", []string{"greenland"}},
	{"GM", "冈比亚", []string{"gambia"}},
	{"GN", "几内亚", []string{"guinea"}},
	{"GP", "瓜德罗普", []string{"guadeloupe"}},
	{"GQ", "赤道几内亚", []string{"equatorial guinea"}},
	{"GR", "希腊", []string{"greece"}},
	{"GS", "南乔治亚和南桑威奇群岛", []string{}},
	{"GT", "危地马拉", []string{"guatemala"}},
	{"GU", "关岛", []string{"guam"}},
	{"GW", "几内亚比绍", []string{}},
	{"GY", "圭亚那", []string{"guyana"}},
	{"HN", "洪都拉斯", []string{"honduras"}},
	{"HR", "克罗地亚", []string{"croatia"}},
	{"HT", "海地", []string{"haiti"}},
	{"HU", "匈牙利", []string{"hungary"}},
	{"ID", "印度尼西亚", []string{"indonesia"}},
	{"IE", "爱尔兰", []string{"ireland"}},
	{"IL", "以色列", []string{"israel"}},
	{"IM", "马恩岛", []string{"isle of man"}},
	{"IN", "印度", []string{"india"}},
	{"IO", "英属印度洋领地", []string{"british indian ocean territory"}},
	{"IQ", "伊拉克", []string{"iraq"}},
	{"IR", "伊朗", []string{"iran"}},
	{"IS", "冰岛", []string{"iceland"}},
	{"IT", "意大利", []string{"italy"}},
	{"JE", "泽西岛", []string{}},
	{"JM", "牙买加", []string{"jamaica"}},
	{"JO", "约旦", []string{}},
	{"JP", "日本", []string{"japan"}},
	{"KE", "肯尼亚", []string{"kenya"}},
	{"KG", "吉尔吉斯斯坦", []string{"kyrgyzstan"}},
	{"KH", "柬埔寨", []string{"cambodia"}},
	{"KI", "基里巴斯", []string{"kiribati"}},
	{"KM", "科摩罗", []string{"comoros"}},
	{"KN", "圣基茨和尼维斯", []string{"saint kitts and nevis"}},
	{"KP", "朝鲜", []string{"north korea"}},
	{"KR", "韩国", []string{"south korea", "korea"}},
	{"KW", "科威特", []string{"kuwait"}},
	{"KY", "开曼群岛", []string{"cayman islands"}},
	{"KZ", "哈萨克斯坦", []string{"kazakhstan"}},
	{"LA", "老挝", []string{"laos"}},
	{"LB", "黎巴嫩", []string{"lebanon"}},
	{"LC", "圣卢西亚", []string{"saint lucia"}},
	{"LI", "列支敦士登", []string{"liechtenstein"}},
	{"LK", "斯里兰卡", []string{"sri lanka"}},
	{"LR", "利比里亚", []string{"liberia"}},
	{"LS", "莱索托", []string{"lesotho"}},
	{"LT", "立陶宛", []string{"lithuania"}},
	{"LU", "卢森堡", []string{"luxembourg"}},
	{"LV", "拉脱维亚", []string{"latvia"}},
	{"LY", "利比亚", []string{"libya"}},
	{"MA", "摩洛哥", []string{"morocco"}},
	{"MC", "摩纳哥", []string{"monaco"}},
	{"MD", "摩尔多瓦", []string{"moldova"}},
	{"ME", "黑山", []string{"montenegro"}},
	{"MF", "法属圣马丁", []string{}},
	{"MG", "马达加斯加", []string{"madagascar"}},
	{"MH", "马绍尔群岛", []string{"marshall islands"}},
	{"MK", "北马其顿", []string{"north macedonia", "macedonia"}},
	{"ML", "马里", []string{"mali"}},
	{"MM", "缅甸", []string{"burma", "myanmar"}},
	{"MN", "蒙古", []string{"mongolia"}},
	{"MP", "北马里亚纳群岛", []string{"northern mariana islands"}},
	{"MQ", "马提尼克", []string{"martinique"}},
	{"MR", "毛里塔尼亚", []string{"mauritania"}},
	{"MS", "蒙特塞拉特", []string{"montserrat"}},
	{"MT", "马耳他", []string{"malta"}},
	{"MU", "毛里求斯", []string{"mauritius"}},
	{"MV", "马尔代夫", []string{"maldives"}},
	{"MW", "马拉维", []string{"malawi"}},
	{"MX", "墨西哥", []string{"mexico"}},
	{"MY", "马来西亚", []string{"malaysia"}},
	{"MZ", "莫桑比克", []string{"mozambique"}},
	{"NA", "纳米比亚", []string{"namibia"}},
	{"NC", "新喀里多尼亚", []string{"new caledonia"}},
	{"NE", "尼日尔", []string{"niger"}},
	{"NF", "诺福克岛", []string{"norfolk island"}},
	{"NG", "尼日利亚", []string{"nigeria"}},
	{"NI", "尼加拉瓜", []string{"nicaragua"}},
	{"NL", "荷兰", []string{"netherlands", "holland"}},
	{"NO", "挪威", []string{"norway"}},
	{"NP", "尼泊尔", []string{"nepal"}},
	{"NR", "瑙鲁", []string{"nauru"}},
	{"NU", "纽埃", []string{"niue"}},
	{"NZ", "新西兰", []string{"new zealand"}},
	{"OM", "阿曼", []string{"oman"}},
	{"PA", "巴拿马", []string{"panama"}},
	{"PE", "秘鲁", []string{"peru"}},
	{"PF", "法属波利尼西亚", []string{"french polynesia"}},
	{"PG", "巴布亚新几内亚", []string{"papua new guinea"}},
	{"PH", "菲律宾", []string{"philippines"}},
	{"PK", "巴基斯坦", []string{"pakistan"}},
	{"PL", "波兰", []string{"poland"}},
	{"PM", "圣皮埃尔和密克隆群岛", []string{}},
	{"PN", "皮特凯恩群岛", []string{"pitcairn islands"}},
	{"PR", "波多黎各", []string{"puerto rico"}},
	{"PS", "巴勒斯坦", []string{"palestinian territories", "palestine"}},
	{"PT", "葡萄牙", []string{"portugal"}},
	{"PW", "帕劳", []string{"palau"}},
	{"PY", "巴拉圭", []string{"paraguay"}},
	{"QA", "卡塔尔", []string{"qatar"}},
	{"RE", "留尼汪", []string{"reunion"}},
	{"RO", "罗马尼亚", []string{"romania"}},
	{"RS", "塞尔维亚", []string{"serbia"}},
	{"RU", "俄罗斯", []string{"russia", "russian federation"}},
	{"RW", "卢旺达", []string{"rwanda"}},
	{"SA", "沙特阿拉伯", []string{"saudi arabia"}},
	{"SB", "所罗门群岛", []string{"solomon islands"}},
	{"SC", "塞舌尔", []string{"seychelles"}},
	{"SD", "苏丹", []string{"sudan"}},
	{"SE", "瑞典", []string{"sweden"}},
	{"SG", "新加坡", []string{"singapore"}},
	{"SH", "圣赫勒拿", []string{}},
	{"SI", "斯洛文尼亚", []string{"slovenia"}},
	{"SJ", "斯瓦尔巴和扬马延", []string{}},
	{"SK", "斯洛伐克", []string{"slovakia"}},
	{"SL", "塞拉利昂", []string{"sierra leone"}},
	{"SM", "圣马力诺", []string{"san marino"}},
	{"SN", "塞内加尔", []string{"senegal"}},
	{"SO", "索马里", []string{"somalia"}},
	{"SR", "苏里南", []string{"suriname"}},
	{"SS", "南苏丹", []string{"south sudan"}},
	{"ST", "圣多美和普林西比", []string{"sao tome and principe"}},
	{"SV", "萨尔瓦多", []string{"el salvador"}},
	{"SX", "荷属圣马丁", []string{"sint maarten"}},
	{"SY", "叙利亚", []string{"syria"}},
	{"SZ", "斯威士兰", []string{"eswatini", "swaziland"}},
	{"TC", "特克斯和凯科斯群岛", []string{}},
	{"TD", "乍得", []string{}},
	{"TF", "法属南部领地", []string{"french southern territories"}},
	{"TG", "多哥", []string{"togo"}},
	{"TH", "泰国", []string{"thailand"}},
	{"TJ", "塔吉克斯坦", []string{"tajikistan"}},
	{"TK", "托克劳", []string{"tokelau"}},
	{"TL", "东帝汶", []string{"east timor"}},
	{"TM", "土库曼斯坦", []string{"turkmenistan"}},
	{"TN", "突尼斯", []string{"tunisia"}},
	{"TO", "汤加", []string{"tonga"}},
	{"TR", "土耳其", []string{"turkey"}},
	{"TT", "特立尼达和多巴哥", []string{"trinidad and tobago"}},
	{"TV", "图瓦卢", []string{"tuvalu"}},
	{"TZ", "坦桑尼亚", []string{"tanzania"}},
	{"UA", "乌克兰", []string{"ukraine"}},
	{"UG", "乌干达", []string{"uganda"}},
	{"US", "美国", []string{"united states", "usa", "america"}},
	{"UY", "乌拉圭", []string{"uruguay"}},
	{"UZ", "乌兹别克斯坦", []string{"uzbekistan"}},
	{"VA", "梵蒂冈", []string{"vatican city", "vatican"}},
	{"VC", "圣文森特和格林纳丁斯", []string{"saint vincent and the grenadines"}},
	{"VE", "委内瑞拉", []string{"venezuela"}},
	{"VG", "英属维尔京群岛", []string{"british virgin islands"}},
	{"VI", "美属维尔京群岛", []string{}},
	{"VN", "越南", []string{"vietnam", "viet nam"}},
	{"VU", "瓦努阿图", []string{"vanuatu"}},
	{"WF", "瓦利斯和富图纳", []string{}},
	{"WS", "萨摩亚", []string{"samoa"}},
	{"XK", "科索沃", []string{"kosovo"}},
	{"YE", "也门", []string{"yemen"}},
	{"YT", "马约特", []string{"mayotte"}},
	{"ZA", "南非", []string{"south africa"}},
	{"ZM", "赞比亚", []string{"zambia"}},
	{"ZW", "津巴布韦", []string{"zimbabwe"}},
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
	// The name that comes first in the label wins, the longest one when
	// several start there: "印度尼西亚" is not India, "美国 马里兰" is not
	// Mali, "South Sudan" is not Sudan. Only then a code standing alone, the
	// first one in the label.
	var c countryInfo
	at, pos := "", -1
	better := func(i int, name string) bool {
		return i >= 0 && (pos < 0 || i < pos || i == pos && len(name) > len(at))
	}
	for _, ci := range countries {
		if i := strings.Index(s, ci.zh); better(i, ci.zh) {
			c, at, pos = ci, ci.zh, i
		}
		for _, e := range ci.en {
			if i := wordIndex(lower, e); better(i, e) {
				c, at, pos = ci, e, i
			}
		}
	}
	for _, f := range fields {
		if at != "" {
			break
		}
		if ci, ok := countryByCode(f); ok {
			c, at = ci, f
		}
	}
	if at != "" {
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
func containsWord(lower, word string) bool { return wordIndex(lower, word) >= 0 }

// wordIndex is where word first stands on its own in lower, or -1.
func wordIndex(lower, word string) int {
	for from := 0; ; {
		i := strings.Index(lower[from:], word)
		if i < 0 {
			return -1
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
			return i
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
