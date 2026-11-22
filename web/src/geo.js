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

// Countries offered when an administrator corrects a node's place. A node
// elsewhere still shows whatever the lookup called its country.
export const COUNTRIES = [
  ['CN', '中国'], ['JP', '日本'], ['KR', '韩国'], ['SG', '新加坡'], ['US', '美国'], ['CA', '加拿大'], ['DE', '德国'], ['GB', '英国'],
  ['FR', '法国'], ['NL', '荷兰'], ['RU', '俄罗斯'], ['AU', '澳大利亚'], ['NZ', '新西兰'], ['IN', '印度'], ['ID', '印度尼西亚'],
  ['MY', '马来西亚'], ['TH', '泰国'], ['VN', '越南'], ['PH', '菲律宾'], ['AE', '阿联酋'], ['TR', '土耳其'], ['IT', '意大利'],
  ['ES', '西班牙'], ['SE', '瑞典'], ['CH', '瑞士'], ['PL', '波兰'], ['BR', '巴西'], ['MX', '墨西哥'], ['ZA', '南非'],
]

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
