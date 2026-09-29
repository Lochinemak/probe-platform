export function ms(v) {
  if (v === undefined || v === null || Number.isNaN(v)) return '-'
  if (v === 0) return '0'
  if (v < 1) return v.toFixed(2)
  if (v < 10) return v.toFixed(2)
  if (v < 100) return v.toFixed(1)
  return Math.round(v).toString()
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

export const typeLabel = { ping: 'PING', tcping: 'TCPING', http: 'HTTP', mtr: 'MTR', dns: 'DNS' }

export function mbps(v) {
  if (v === undefined || v === null) return '-'
  return v >= 100 ? Math.round(v) + ' Mbps' : v.toFixed(1) + ' Mbps'
}

// Compact label for an error message; the full text is shown in a popover.
export function errorLabel(msg) {
  const m = String(msg || '').toLowerCase()
  if (!m) return '失败'
  if (m.includes('fake-ip')) return 'fake-IP'
  if (m.includes('timeout') || m.includes('deadline')) return '超时'
  if (m.includes('disconnected')) return '节点断开'
  if (m.includes('offline')) return '节点离线'
  if (m.includes('cancelled')) return '已取消'
  if (m.includes('no such host')) return '域名不存在'
  if (m.includes('refused')) return '连接被拒'
  if (m.includes('unreachable')) return '不可达'
  if (m.includes('not permitted') || m.includes('raw icmp') || m.includes('cap_net_raw')) return '无权限'
  if (m.includes('certificate') || m.includes('x509') || m.includes('tls')) return 'TLS 错误'
  return '失败'
}

export function statusLabel(s) {
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
