export function ms(v) {
  if (v === undefined || v === null || Number.isNaN(v)) return '-'
  if (v === 0) return '0'
  if (v < 1) return v.toFixed(2)
  if (v < 10) return v.toFixed(2)
  if (v < 100) return v.toFixed(1)
  return Math.round(v).toString()
}

// dur shows a duration with its unit: milliseconds up to a second, seconds
// beyond (HTTP requests routinely take several).
export function dur(v) {
  if (v === undefined || v === null || Number.isNaN(v)) return '-'
  return v >= 1000 ? (v / 1000).toFixed(2) + ' s' : ms(v) + ' ms'
}

export function pct(v) {
  if (v === undefined || v === null) return '-'
  return (Math.round(v * 10) / 10) + '%'
}

export function lossClass(v) {
  if (v === undefined || v === null) return ''
  if (v <= 0) return 'ok'
  if (v < 20) return 'warn'
  return 'bad'
}

export function rttClass(v) {
  if (v === undefined || v === null) return ''
  if (v < 50) return 'ok'
  if (v < 150) return 'warn'
  return 'bad'
}

// size shows a byte count in binary units up to terabytes (memory, disks).
export function size(n) {
  if (n === undefined || n === null || !(n > 0)) return '-'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++ }
  return (n >= 100 || i === 0 ? Math.round(n) : n.toFixed(1)) + ' ' + units[i]
}

export function uptime(sec) {
  if (!(sec > 0)) return '-'
  const d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600), m = Math.floor((sec % 3600) / 60)
  return d ? `${d} 天 ${h} 小时` : h ? `${h} 小时 ${m} 分` : `${m} 分钟`
}

export function bytes(n) {
  if (n === undefined || n === null || n < 0) return '-'
  if (n < 1024) return n + ' B'
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB'
  return (n / 1024 / 1024).toFixed(2) + ' MB'
}

export function fmtTime(iso) {
  if (!iso) return '-'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '-'
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

export function timeAgo(iso) {
  if (!iso) return '-'
  const diff = (Date.now() - new Date(iso).getTime()) / 1000
  if (diff < 5) return '刚刚'
  if (diff < 60) return `${Math.floor(diff)} 秒前`
  if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`
  if (diff < 86400) return `${Math.floor(diff / 3600)} 小时前`
  return `${Math.floor(diff / 86400)} 天前`
}

export const typeLabel = { ping: 'PING', tcping: 'TCPING', http: 'HTTP', mtr: 'MTR', dns: 'DNS', udp: 'UDP', tls: 'TLS', dns_compare: 'DNS 对比', status: '节点状态' }

export function mbps(v) {
  if (v === undefined || v === null) return '-'
  return v >= 100 ? Math.round(v) + ' Mbps' : v.toFixed(1) + ' Mbps'
}

// errorCategory sorts an error message into one of a few kinds: { key, label }.
// The label is what tables show; the full text opens in a popover. Order
// matters: the more specific cause is tested first ("lookup x: i/o timeout"
// is a DNS failure, "dial tcp: i/o timeout" a connect timeout).
const ERROR_KINDS = [
  ['fakeip', 'fake-IP', ['fake-ip']],
  ['stopped', '已停止', ['stopped']],
  ['cancelled', '已取消', ['cancelled', 'canceled']],
  ['offline', '节点离线', ['agent offline', 'agent disconnected', 'disconnected', 'send queue full']],
  ['unsupported', '节点不支持', ['unknown task type', 'unsupported', 'not supported', '节点版本过旧']],
  ['denied', '无权限', ['not permitted', 'raw icmp', 'cap_net_raw', 'permission denied']],
  ['policy', '目标受限', ['不是公网地址', 'not a public address', 'public_only']],
  ['dns', 'DNS 失败', ['no such host', 'nxdomain', 'lookup ', 'server misbehaving', 'no answer', 'rcode']],
  ['tls', 'TLS 失败', ['certificate', 'x509', 'tls:', 'tls handshake', 'handshake failure']],
  ['closed', '端口关闭', ['port unreachable']],
  ['refused', '端口拒绝', ['connection refused', 'refused']],
  ['reset', '连接重置', ['connection reset', 'reset by peer', 'broken pipe', 'unexpected eof']],
  ['unreachable', '网络不可达', ['network is unreachable', 'no route to host', 'unreachable']],
  ['connect_timeout', '连接超时', ['dial tcp', 'connect: ', 'connection timed out']],
  ['timeout', '超时', ['timeout', 'timed out', 'deadline exceeded']],
  ['loss', '全部丢包', ['100% loss']],
  ['noreply', '无响应', ['no reply']],
]
export function errorCategory(msg) {
  const m = String(msg || '').toLowerCase()
  for (const [key, label, words] of ERROR_KINDS) {
    if (!words.some((w) => m.includes(w))) continue
    // "dial tcp …" only says what was being done; it is a connect timeout
    // when it also timed out, otherwise keep looking.
    if (key === 'connect_timeout' && !/timeout|timed out|deadline/.test(m)) continue
    return { key, label }
  }
  return { key: 'error', label: '失败' }
}
export function errorLabel(msg) { return errorCategory(msg).label }

// Latency colour scale, shared by the tables, the legend and the maps. A link
// RTT (ping, tcping, dns, mtr) is graded in milliseconds; a whole HTTP request
// takes far longer and is graded in seconds. bucketOf returns 0 (best) … 6
// (worst), or -1 when there is no value.
const BUCKETS = {
  ms: { edges: [50, 100, 150, 200, 250, 300], labels: ['≤50ms', '≤100ms', '≤150ms', '≤200ms', '≤250ms', '≤300ms', '>300ms'] },
  s: { edges: [500, 1000, 2000, 3000, 5000, 10000], labels: ['≤0.5s', '≤1s', '≤2s', '≤3s', '≤5s', '≤10s', '>10s'] },
}
export function latencyScale(type) { return BUCKETS[type === 'http' ? 's' : 'ms'] }
export function bucketOf(type, v) {
  if (v === undefined || v === null || Number.isNaN(v)) return -1
  const i = latencyScale(type).edges.findIndex((e) => v <= e)
  return i < 0 ? 6 : i
}
export function bucketClass(type, v) {
  const b = bucketOf(type, v)
  return b < 0 ? '' : 'lat-' + b
}

// statusLabel takes a status or a whole result; a result that finished
// because the user stopped the task (it keeps its partial data) says so.
export function statusLabel(s) {
  if (s && typeof s === 'object') {
    if (s.status === 'done' && s.error === 'stopped') return '已停止'
    s = s.status
  }
  return { pending: '等待', running: '进行中', done: '完成', error: '失败' }[s] || s
}

export function agentPlace(r) {
  return [r.location || r.geo_location, r.isp || r.geo_isp].filter(Boolean).join(' · ')
}

// Custom HTTP headers are edited as one "Key: Value" per line and sent as a map.
export function parseHeaders(text) {
  const headers = {}
  for (const line of String(text || '').split('\n')) {
    const i = line.indexOf(':')
    if (i > 0) headers[line.slice(0, i).trim()] = line.slice(i + 1).trim()
  }
  return headers
}

export function formatHeaders(headers) {
  return Object.entries(headers || {}).map(([k, v]) => `${k}: ${v}`).join('\n')
}
