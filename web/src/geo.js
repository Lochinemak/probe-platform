// Node geography as the server reports it (AgentStatus.geo / AgentResult.geo:
// { cc, country, province, city, line, src }) and the fixed vocabularies the
// filters, groupings and maps share with internal/server/geodata.go.

export const LINES = ['电信', '联通', '移动', '广电', '教育网', 'IDC', '云厂商', '其他']

// Province-level divisions by short name, grouped the way results are.
export const REGIONS = [
  { name: '华北', provinces: ['北京', '天津', '河北', '山西', '内蒙古'] },
  { name: '东北', provinces: ['辽宁', '吉林', '黑龙江'] },
  { name: '华东', provinces: ['上海', '江苏', '浙江', '安徽', '福建', '江西', '山东'] },
  { name: '华中', provinces: ['河南', '湖北', '湖南'] },
  { name: '华南', provinces: ['广东', '广西', '海南'] },
  { name: '西南', provinces: ['重庆', '四川', '贵州', '云南', '西藏'] },
  { name: '西北', provinces: ['陕西', '甘肃', '青海', '宁夏', '新疆'] },
  { name: '港澳台', provinces: ['香港', '澳门', '台湾'] },
]
export const PROVINCES = REGIONS.flatMap((r) => r.provinces)
const REGION_OF = Object.fromEntries(REGIONS.flatMap((r) => r.provinces.map((p) => [p, r.name])))

// Countries offered when an administrator corrects a node's place: the usual
// ones first, then every other one by pinyin. The same codes and names as
// internal/server/geodata.go countries (a Go test compares them); Hong Kong,
// Macau and Taiwan are provinces of CN here.
export const COMMON_COUNTRIES = [
  ['CN', '中国'], ['JP', '日本'], ['KR', '韩国'], ['SG', '新加坡'], ['US', '美国'], ['CA', '加拿大'], ['GB', '英国'], ['DE', '德国'],
  ['FR', '法国'], ['NL', '荷兰'], ['RU', '俄罗斯'], ['AU', '澳大利亚'], ['NZ', '新西兰'], ['IN', '印度'], ['ID', '印度尼西亚'], ['MY', '马来西亚'],
  ['TH', '泰国'], ['VN', '越南'], ['PH', '菲律宾'], ['AE', '阿联酋'], ['TR', '土耳其'], ['IT', '意大利'], ['ES', '西班牙'], ['SE', '瑞典'],
  ['CH', '瑞士'], ['PL', '波兰'], ['BR', '巴西'], ['MX', '墨西哥'], ['ZA', '南非'],
]
export const OTHER_COUNTRIES = [
  ['AL', '阿尔巴尼亚'], ['DZ', '阿尔及利亚'], ['AF', '阿富汗'], ['AR', '阿根廷'], ['AW', '阿鲁巴'], ['OM', '阿曼'], ['AZ', '阿塞拜疆'], ['EG', '埃及'],
  ['ET', '埃塞俄比亚'], ['IE', '爱尔兰'], ['EE', '爱沙尼亚'], ['AD', '安道尔'], ['AO', '安哥拉'], ['AI', '安圭拉'], ['AG', '安提瓜和巴布达'], ['AT', '奥地利'],
  ['AX', '奥兰群岛'], ['BB', '巴巴多斯'], ['PG', '巴布亚新几内亚'], ['BS', '巴哈马'], ['PK', '巴基斯坦'], ['PY', '巴拉圭'], ['PS', '巴勒斯坦'], ['BH', '巴林'],
  ['PA', '巴拿马'], ['BY', '白俄罗斯'], ['BM', '百慕大'], ['BG', '保加利亚'], ['MP', '北马里亚纳群岛'], ['MK', '北马其顿'], ['BJ', '贝宁'], ['BE', '比利时'],
  ['IS', '冰岛'], ['PR', '波多黎各'], ['BA', '波斯尼亚和黑塞哥维那'], ['BO', '玻利维亚'], ['BZ', '伯利兹'], ['BW', '博茨瓦纳'], ['BT', '不丹'], ['BF', '布基纳法索'],
  ['BI', '布隆迪'], ['KP', '朝鲜'], ['GQ', '赤道几内亚'], ['DK', '丹麦'], ['TL', '东帝汶'], ['TG', '多哥'], ['DO', '多米尼加共和国'], ['DM', '多米尼克'],
  ['EC', '厄瓜多尔'], ['ER', '厄立特里亚'], ['FO', '法罗群岛'], ['PF', '法属波利尼西亚'], ['GF', '法属圭亚那'], ['TF', '法属南部领地'], ['MF', '法属圣马丁'], ['VA', '梵蒂冈'],
  ['FJ', '斐济'], ['FI', '芬兰'], ['CV', '佛得角'], ['FK', '福克兰群岛'], ['GM', '冈比亚'], ['CG', '刚果（布）'], ['CD', '刚果（金）'], ['CO', '哥伦比亚'],
  ['CR', '哥斯达黎加'], ['GD', '格林纳达'], ['GL', '格陵兰'], ['GE', '格鲁吉亚'], ['GG', '根西岛'], ['CU', '古巴'], ['GP', '瓜德罗普'], ['GU', '关岛'],
  ['GY', '圭亚那'], ['KZ', '哈萨克斯坦'], ['HT', '海地'], ['BQ', '荷属加勒比区'], ['SX', '荷属圣马丁'], ['ME', '黑山'], ['HN', '洪都拉斯'], ['KI', '基里巴斯'],
  ['DJ', '吉布提'], ['KG', '吉尔吉斯斯坦'], ['GN', '几内亚'], ['GW', '几内亚比绍'], ['GH', '加纳'], ['GA', '加蓬'], ['KH', '柬埔寨'], ['CZ', '捷克'],
  ['ZW', '津巴布韦'], ['CM', '喀麦隆'], ['QA', '卡塔尔'], ['KY', '开曼群岛'], ['CC', '科科斯群岛'], ['KM', '科摩罗'], ['XK', '科索沃'], ['CI', '科特迪瓦'],
  ['KW', '科威特'], ['HR', '克罗地亚'], ['KE', '肯尼亚'], ['CK', '库克群岛'], ['CW', '库拉索'], ['LV', '拉脱维亚'], ['LS', '莱索托'], ['LA', '老挝'],
  ['LB', '黎巴嫩'], ['LT', '立陶宛'], ['LR', '利比里亚'], ['LY', '利比亚'], ['LI', '列支敦士登'], ['RE', '留尼汪'], ['LU', '卢森堡'], ['RW', '卢旺达'],
  ['RO', '罗马尼亚'], ['MG', '马达加斯加'], ['IM', '马恩岛'], ['MV', '马尔代夫'], ['MT', '马耳他'], ['MW', '马拉维'], ['ML', '马里'], ['MH', '马绍尔群岛'],
  ['MQ', '马提尼克'], ['YT', '马约特'], ['MU', '毛里求斯'], ['MR', '毛里塔尼亚'], ['AS', '美属萨摩亚'], ['VI', '美属维尔京群岛'], ['MN', '蒙古'], ['MS', '蒙特塞拉特'],
  ['BD', '孟加拉国'], ['PE', '秘鲁'], ['FM', '密克罗尼西亚'], ['MM', '缅甸'], ['MD', '摩尔多瓦'], ['MA', '摩洛哥'], ['MC', '摩纳哥'], ['MZ', '莫桑比克'],
  ['NA', '纳米比亚'], ['GS', '南乔治亚和南桑威奇群岛'], ['SS', '南苏丹'], ['NR', '瑙鲁'], ['NI', '尼加拉瓜'], ['NP', '尼泊尔'], ['NE', '尼日尔'], ['NG', '尼日利亚'],
  ['NU', '纽埃'], ['NO', '挪威'], ['NF', '诺福克岛'], ['PW', '帕劳'], ['PN', '皮特凯恩群岛'], ['PT', '葡萄牙'], ['SV', '萨尔瓦多'], ['WS', '萨摩亚'],
  ['RS', '塞尔维亚'], ['SL', '塞拉利昂'], ['SN', '塞内加尔'], ['CY', '塞浦路斯'], ['SC', '塞舌尔'], ['SA', '沙特阿拉伯'], ['BL', '圣巴泰勒米'], ['CX', '圣诞岛'],
  ['ST', '圣多美和普林西比'], ['SH', '圣赫勒拿'], ['KN', '圣基茨和尼维斯'], ['LC', '圣卢西亚'], ['SM', '圣马力诺'], ['PM', '圣皮埃尔和密克隆群岛'], ['VC', '圣文森特和格林纳丁斯'], ['LK', '斯里兰卡'],
  ['SK', '斯洛伐克'], ['SI', '斯洛文尼亚'], ['SJ', '斯瓦尔巴和扬马延'], ['SZ', '斯威士兰'], ['SD', '苏丹'], ['SR', '苏里南'], ['SB', '所罗门群岛'], ['SO', '索马里'],
  ['TJ', '塔吉克斯坦'], ['TZ', '坦桑尼亚'], ['TO', '汤加'], ['TC', '特克斯和凯科斯群岛'], ['TT', '特立尼达和多巴哥'], ['TN', '突尼斯'], ['TV', '图瓦卢'], ['TM', '土库曼斯坦'],
  ['TK', '托克劳'], ['WF', '瓦利斯和富图纳'], ['VU', '瓦努阿图'], ['GT', '危地马拉'], ['VE', '委内瑞拉'], ['BN', '文莱'], ['UG', '乌干达'], ['UA', '乌克兰'],
  ['UY', '乌拉圭'], ['UZ', '乌兹别克斯坦'], ['EH', '西撒哈拉'], ['GR', '希腊'], ['NC', '新喀里多尼亚'], ['HU', '匈牙利'], ['SY', '叙利亚'], ['JM', '牙买加'],
  ['AM', '亚美尼亚'], ['YE', '也门'], ['IQ', '伊拉克'], ['IR', '伊朗'], ['IL', '以色列'], ['VG', '英属维尔京群岛'], ['IO', '英属印度洋领地'], ['JO', '约旦'],
  ['ZM', '赞比亚'], ['JE', '泽西岛'], ['TD', '乍得'], ['GI', '直布罗陀'], ['CL', '智利'], ['CF', '中非共和国'],
]
export const COUNTRIES = [...COMMON_COUNTRIES, ...OTHER_COUNTRIES]

export const UNKNOWN_PLACE = '未识别'

// regionName is the result grouping a node falls in: 华东 … 港澳台 for China,
// 海外 elsewhere, 未识别 when its place is not known.
export function regionName(geo) {
  if (!geo?.cc) return UNKNOWN_PLACE
  if (geo.cc !== 'CN') return '海外'
  return REGION_OF[geo.province] || '中国'
}

export function isMainland(geo) {
  return geo?.cc === 'CN' && !['香港', '澳门', '台湾'].includes(geo.province)
}

// placeKey is the province (China) or country (elsewhere) a node is filed under.
export function placeKey(geo) {
  if (!geo?.cc) return UNKNOWN_PLACE
  return geo.cc === 'CN' ? geo.province || '中国' : geo.country || geo.cc
}

// placeLabel is the short place text shown next to a node: "广东 深圳", "日本 东京".
export function placeLabel(geo) {
  if (!geo?.cc) return ''
  const head = geo.cc === 'CN' ? geo.province || '中国' : geo.country || geo.cc
  const tail = geo.cc === 'CN' ? geo.city : geo.city || geo.province
  return tail && tail !== head ? `${head} ${tail}` : head
}

// placeOrder sorts place keys: Chinese provinces in REGIONS order, then other
// countries by name, then the unknown bucket.
export function placeOrder(a, b) {
  const rank = (k) => {
    if (k === UNKNOWN_PLACE) return 9000
    if (k === '中国') return 500
    const i = PROVINCES.indexOf(k)
    return i >= 0 ? i : 1000
  }
  return rank(a) - rank(b) || a.localeCompare(b, 'zh-CN')
}
