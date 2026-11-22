// Per-type rules for what may go in the target box, checked before a task is
// created so a typo gets a specific message instead of every node failing.
// The server and the agents validate again; this is only for the user's benefit.

// Labels, page titles and input help for each probe type.
export const TYPE_INFO = {
  ping: { label: 'ICMP Ping', title: '多地 Ping 测试', desc: 'ICMP 延迟与丢包', placeholder: '请输入域名或 IP', hint: 'example.com 或 1.1.1.1' },
  tcping: { label: 'TCP 端口', title: '多地 TCP 连接测试', desc: 'TCP 端口连通性与握手延迟', placeholder: '请输入域名、IP 或 host:port', hint: 'example.com 或 example.com:443 · 不带端口时用右侧的端口' },
  http: { label: 'HTTP 检测', title: '多地 HTTP 响应测试', desc: 'HTTP(S) 状态码与分阶段耗时', placeholder: '请输入域名、IP 或完整网址', hint: 'https://example.com 或 example.com/path' },
  dns: { label: 'DNS 解析', title: '多地 DNS 解析测试', desc: '各节点解析结果对比：记录、TTL、RCode、耗时', placeholder: '请输入域名或 IP，不要带端口', hint: 'example.com · 查 PTR 时填 IP · 不要填写端口、协议或路径' },
  udp: { label: 'UDP 探测', title: '多地 UDP 探测', desc: '向 UDP 端口发一个数据报，看有没有回应', placeholder: '请输入 host:port', hint: 'example.com:53 或 8.8.8.8:53 · 53 端口发 DNS 查询，其它端口发一段文本' },
  tls: { label: 'TLS 证书', title: '多地 TLS 证书测试', desc: 'TLS 握手耗时、协议与套件、证书链与到期时间', placeholder: '请输入域名或 IP', hint: 'example.com 或 example.com:8443 · 不填端口默认 443' },
  dns_compare: { label: 'DNS 对比', title: '多地 DNS 对比测试', desc: '每个节点同时问多个解析器，看结果是否一致', placeholder: '请输入域名，不要带端口', hint: 'example.com · 多解析器对比，不要填写端口' },
  status: { label: '节点状态', title: '节点状态测试', desc: '节点自身的系统、负载、内存与磁盘（仅管理员）', placeholder: '无需输入目标', hint: '无需输入目标，直接查询选中节点自身的状态' },
  mtr: { label: '路由分析', title: '多地路由追踪测试', desc: '逐跳路由追踪，ICMP / TCP / UDP 三种探针（节点需具备 raw socket 权限）', placeholder: '请输入域名或 IP', hint: 'example.com 或 1.1.1.1 · 统计每一跳的丢包和延迟' },
}

const NAME = { ping: 'Ping 目标', tcping: 'TCP 检测目标', http: 'HTTP 检测目标', dns: 'DNS 解析目标', mtr: '路由分析目标', udp: 'UDP 探测目标', tls: 'TLS 检测目标', dns_compare: 'DNS 对比目标' }

export function isIPv4(s) {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(s)
  return !!m && m.slice(1).every((p) => Number(p) <= 255)
}

export function isIPv6(s) {
  s = s.replace(/%[\w.-]+$/, '') // zone id
  if (!s.includes(':') || !/^[0-9a-fA-F:.]+$/.test(s)) return false
  if ((s.match(/::/g) || []).length > 1 || s.includes(':::')) return false
  let groups = s.split(':')
  const last = groups[groups.length - 1]
  if (last.includes('.')) { // embedded IPv4 counts as two groups
    if (!isIPv4(last)) return false
    groups = [...groups.slice(0, -1), '0', '0']
  }
  if (groups.some((g) => g.length > 4)) return false
  return s.includes('::') ? groups.length <= 8 : groups.length === 8
}

// A hostname as people type it: dot-separated labels, underscores tolerated
// (SRV / DKIM names), internationalised names allowed, optional trailing dot.
export function isHostname(s) {
  if (!s || s.length > 253) return false
  const labels = s.replace(/\.$/, '').split('.')
  return labels.every((l) => l.length > 0 && l.length <= 63 && /^[\p{L}\p{N}_]([\p{L}\p{N}_-]*[\p{L}\p{N}_])?$/u.test(l))
}

function checkPort(p) {
  return /^\d{1,5}$/.test(p) && Number(p) >= 1 && Number(p) <= 65535
}

// splitHostPort understands host, host:port, v6, [v6] and [v6]:port.
// port is '' when absent; ok is false when the port part is not a port.
export function splitHostPort(s) {
  let m = /^\[([^\]]+)\](?::(.*))?$/.exec(s)
  if (m) return { host: m[1], port: m[2] ?? '', bracketed: true, ok: m[2] === undefined || checkPort(m[2]) }
  if (isIPv6(s)) return { host: s, port: '', ok: true }
  m = /^([^:]+):(.*)$/.exec(s)
  if (m) return { host: m[1], port: m[2], ok: checkPort(m[2]) }
  return { host: s, port: '', ok: true }
}

function hostError(name, host) {
  if (!host) return `${name}不能为空`
  if (isIPv4(host) || isIPv6(host) || isHostname(host)) return ''
  return `${name}格式不正确，请填写域名或 IP`
}

// validateTarget checks raw for the given probe type. On success it returns
// { ok: true, target } with the value to send (trimmed; for http a full URL
// using opts.scheme when none was typed) and, for tcping, the port that was
// part of the input. On failure { ok: false, error } carries a message to show.
export function validateTarget(type, raw, opts = {}) {
  const name = NAME[type] || '探测目标'
  let t = String(raw || '').trim()
  const fail = (error) => ({ ok: false, error })
  if (type === 'status') return { ok: true, target: 'self' } // the one probe without a target
  if (!t) return fail('请输入探测目标')
  if (t.length > 2048) return fail('探测目标不能超过 2048 个字符')
  if (/\s/.test(t)) return fail(`${name}不能包含空白字符`)

  if (type === 'http') {
    let url = t
    const scheme = /^([a-zA-Z][a-zA-Z0-9+.-]*):\/\//.exec(t)
    if (scheme) {
      if (!['http', 'https'].includes(scheme[1].toLowerCase())) return fail('HTTP 检测只支持 http 或 https')
    } else {
      url = `${opts.scheme === 'http' ? 'http' : 'https'}://${t}`
    }
    let u
    try { u = new URL(url) } catch { return fail('请输入有效的网址，例如 https://example.com/path') }
    if (u.username || u.password) return fail('网址里不能包含用户名或密码')
    if (u.hash) return fail('网址里不能包含 # 片段')
    if (!u.hostname) return fail('请输入网址的主机名')
    const port = /^[a-z]+:\/\/[^/?#]*:(\d*)(?:[/?#]|$)/i.exec(url)
    if (port && !checkPort(port[1])) return fail('端口必须是 1-65535 的数字')
    return { ok: true, target: url, scheme: u.protocol.replace(':', '') }
  }

  // A pasted https:// address is what people have at hand for a certificate
  // check; the scheme is dropped, a path is not accepted.
  if (type === 'tls' && /^https:\/\//i.test(t)) t = t.replace(/^https:\/\//i, '').replace(/\/$/, '')
  if (/^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//.test(t)) return fail(`${name}只需要填写域名或 IP，不要包含协议`)
  if (/[/?#@]/.test(t)) return fail(`${name}不能包含路径、查询参数或用户信息`)

  if (type === 'tcping' || type === 'tls' || type === 'udp') {
    const hp = splitHostPort(t)
    if (!hp.ok) return fail('端口必须是 1-65535 的数字')
    const err = hostError(type === 'tcping' ? 'TCP 检测主机' : type === 'tls' ? 'TLS 检测主机' : 'UDP 探测主机', hp.host)
    if (err) return fail(err)
    if (type === 'udp' && !hp.port && !opts.port) return fail('UDP 探测目标必须写成 host:port，例如 example.com:53')
    return { ok: true, target: t, port: hp.port ? Number(hp.port) : 0 }
  }

  // ping, dns, dns_compare, mtr: a bare host. Brackets around an IPv6 literal are dropped.
  const hp = splitHostPort(t)
  if (hp.port !== '' || (!hp.ok && !hp.bracketed)) return fail(`${name}不能包含端口，请只输入域名或 IP`)
  const err = hostError(name, hp.host)
  if (err) return fail(err)
  return { ok: true, target: hp.host }
}
