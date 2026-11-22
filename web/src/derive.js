// Turns a raw AgentResult (final data or streamed progress) into what the
// tables render, so live and historical views share one code path.
import { errorCategory } from './fmt.js'
import { placeLabel, placeKey, regionName } from './geo.js'

// percentile by the nearest-rank method: the smallest sample that at least
// p percent of the samples do not exceed.
export function percentile(values, p) {
  if (!values.length) return undefined
  const sorted = [...values].sort((a, b) => a - b)
  return sorted[Math.max(0, Math.ceil((p / 100) * sorted.length) - 1)]
}

function statsOf(replies) {
  const sent = replies.length
  const ok = replies.filter((r) => r.ok)
  const rtts = ok.map((r) => r.rtt_ms)
  const s = { sent, received: ok.length, loss_pct: sent ? ((sent - ok.length) / sent) * 100 : 0 }
  if (rtts.length) {
    s.min_ms = Math.min(...rtts)
    s.max_ms = Math.max(...rtts)
    s.avg_ms = rtts.reduce((a, b) => a + b, 0) / rtts.length
    s.stddev_ms = Math.sqrt(rtts.reduce((a, b) => a + (b - s.avg_ms) ** 2, 0) / rtts.length)
  }
  return s
}

export function derive(task, r) {
  const out = { ...r, live: r.status === 'running' || r.status === 'pending' }
  const data = r.data && typeof r.data === 'object' ? r.data : null
  const progress = r.progress || []
  const resolved = progress.find((p) => p.kind === 'resolved')
  const resolvedIP = resolved?.data || ''
  switch (task.type) {
    case 'tls': {
      let attempts = data?.attempts
      if (!attempts) attempts = progress.filter((p) => p.kind === 'attempt').map((p) => p.data)
      out.attempts = attempts
      out.last = attempts[attempts.length - 1] || null
      const ok = attempts.filter((a) => a.ok).map((a) => a.handshake_ms)
      out.stats = data?.stats || { sent: attempts.length, received: ok.length, avg_ms: ok.length ? ok.reduce((a, b) => a + b, 0) / ok.length : undefined }
      out.tls = data || null // version, cipher, chain, verified … once the node has reported
      out.leaf = data?.chain?.[0] || null
      out.ip = data?.ip || resolvedIP
      out.port = data?.port
      break
    }
    case 'dns_compare': {
      let entries = data?.entries
      if (!entries) entries = progress.filter((p) => p.kind === 'entry').map((p) => p.data)
      out.entries = entries
      const answered = entries.filter((e) => e.attempt?.ok)
      const keys = [...new Set(answered.map((e) => e.key))]
      out.verdict = data?.verdict || (r.status === 'running' ? '' : answered.length ? (keys.length > 1 ? 'inconsistent' : 'consistent') : 'failed')
      out.groups = data?.groups ?? keys.length
      // Groups are numbered by size, so "1" is what most resolvers said.
      const count = (k) => answered.filter((e) => e.key === k).length
      out.groupOf = Object.fromEntries(keys.sort((a, b) => count(b) - count(a) || a.localeCompare(b)).map((k, i) => [k, i + 1]))
      out.slowest = answered.length ? Math.max(...answered.map((e) => e.attempt.rtt_ms)) : undefined
      break
    }
    case 'status':
      out.sys = data || null
      break
    case 'ping':
    case 'udp':
    case 'tcping': {
      let replies = data?.replies
      if (!replies) replies = progress.filter((p) => p.kind === 'reply').map((p) => p.data)
      replies = [...replies].sort((a, b) => a.seq - b.seq)
      out.replies = replies
      out.stats = data?.stats || statsOf(replies)
      out.ip = data?.ip || resolvedIP
      out.port = data?.port
      {
        const ok = replies.filter((x) => x.ok)
        out.p95 = percentile(ok.map((x) => x.rtt_ms), 95)
        out.latest = ok.length ? ok[ok.length - 1].rtt_ms : undefined
      }
      break
    }
    case 'http': {
      let attempts = data?.attempts
      if (!attempts) attempts = progress.filter((p) => p.kind === 'attempt').map((p) => p.data)
      out.attempts = attempts
      out.last = attempts[attempts.length - 1] || null
      const okTimes = attempts.filter((a) => a.ok).map((a) => a.timing?.total_ms || 0)
      out.stats = data?.stats || {
        sent: attempts.length,
        received: okTimes.length,
        avg_ms: okTimes.length ? okTimes.reduce((a, b) => a + b, 0) / okTimes.length : undefined,
      }
      // Average phase timings across successful attempts for the summary row.
      const phases = ['dns_ms', 'connect_ms', 'tls_ms', 'ttfb_ms', 'transfer_ms', 'total_ms']
      const okA = attempts.filter((a) => a.ok)
      out.assertFail = attempts.some((a) => a.ok && a.assert_ok === false)
      out.failedAssertions = attempts.flatMap((a) => (a.assertions || []).filter((x) => !x.pass).map((x) => `${x.name}：${x.detail}`))
      const speeds = okA.map((a) => a.throughput_mbps).filter((v) => v > 0)
      out.throughput = speeds.length ? speeds.reduce((s, v) => s + v, 0) / speeds.length : undefined
      out.avg = {}
      for (const k of phases) out.avg[k] = okA.length ? okA.reduce((s, a) => s + (a.timing?.[k] || 0), 0) / okA.length : undefined
      // The node's DNS is hijacked by a fake-IP proxy: the request went through
      // it, so the result is real but the timings are of the proxy path.
      out.fakeIP = attempts.some((a) => a.fake_ip)
      break
    }
    case 'dns': {
      let attempts = data?.attempts
      if (!attempts) attempts = progress.filter((p) => p.kind === 'attempt').map((p) => p.data)
      out.attempts = attempts
      out.last = attempts[attempts.length - 1] || null
      out.stats = data?.stats || { sent: attempts.length, received: attempts.filter((a) => a.ok).length }
      out.fakeIP = attempts.some((a) => a.fake_ip)
      break
    }
    case 'mtr': {
      let hops = data?.hops
      if (!hops) {
        const last = [...progress].reverse().find((p) => p.kind === 'hops')
        hops = last ? last.data : []
      }
      out.hops = hops
      out.ip = data?.ip || resolvedIP
      out.reached = data?.reached ?? hops.some((h) => h.reached)
      out.rounds = data?.rounds
      out.protocol = data?.protocol || task.params?.protocol || 'icmp'
      out.port = data?.port || task.params?.port
      // The summary line of a trace: its last hop, i.e. the target when it
      // was reached, else the farthest router that answered.
      out.lastHop = [...hops].reverse().find((h) => h.reached) || [...hops].reverse().find((h) => h.received) || null
      out.hopCount = out.lastHop ? out.lastHop.ttl : hops.length
      out.sampled = data?.rounds ?? Math.max(0, ...hops.map((h) => h.sent || 0))
      break
    }
  }
  finish(task, r, out, resolved)
  return out
}

// finish adds what every table, filter and summary needs whatever the probe
// type: where the node is, the address it probed, one headline latency
// (`metric`, ms), whether the probe failed and a short status text.
function finish(task, r, out, resolved) {
  out.geo = r.geo || null
  out.line = r.geo?.line || ''
  out.place = placeLabel(r.geo) || r.location || ''
  out.placeKey = placeKey(r.geo)
  out.region = regionName(r.geo)
  if (task.type === 'http') out.ip = out.last?.ip || r.target_ip || ''
  out.ip = out.ip || r.target_ip || ''
  out.ipGeo = r.target_geo || resolved?.geo || ''
  // Every address this node got for the target, for the response-IP summary:
  // the one it probed, or for a DNS lookup each address record it was given.
  const addresses = (answers) => (answers || []).filter((a) => (a.type === 'A' || a.type === 'AAAA') && a.value).map((a) => ({ ip: a.value, geo: a.geo || '' }))
  if (task.type === 'dns') out.ips = addresses(out.last?.answers)
  else if (task.type === 'dns_compare') {
    // Each address once per node, however many of its resolvers returned it.
    const seen = new Map()
    for (const e of out.entries) for (const a of addresses(e.attempt?.answers)) if (!seen.has(a.ip)) seen.set(a.ip, a)
    out.ips = [...seen.values()]
  } else out.ips = out.ip ? [{ ip: out.ip, geo: out.ipGeo }] : []

  let metric, failed = r.status === 'error', why = r.error || ''
  switch (task.type) {
    case 'ping':
    case 'udp':
    case 'tcping':
      if (out.stats?.received) metric = out.stats.avg_ms
      if (r.status === 'done' && out.stats?.sent && !out.stats.received) {
        failed = true
        // UDP says why nothing came back: the port is closed, or silence.
        why = why || [...(out.replies || [])].reverse().find((x) => x.error)?.error || '100% loss'
      }
      break
    case 'tls':
      if (out.stats?.received) metric = out.stats.avg_ms
      if (out.last && !out.last.ok) { failed = true; why = why || out.last.error || '' }
      else if (out.tls && !out.tls.verified) { failed = true; why = why || 'x509: ' + (out.tls.verify_error || 'certificate verification failed') }
      else if (out.tls && out.tls.cert_ok === false) { failed = true; why = why || `证书还有 ${out.tls.days_left} 天到期，低于设定的 ${task.params?.expect_cert_days} 天` }
      break
    case 'dns_compare':
      metric = out.slowest
      if (r.status === 'done' && out.verdict === 'failed') { failed = true; why = why || out.entries.find((e) => e.attempt?.error)?.attempt.error || 'no answer' }
      break
    case 'http':
      metric = out.avg?.total_ms
      if (out.last && !out.last.ok) { failed = true; why = why || out.last.error || '' }
      else if (out.assertFail) { failed = true; why = why || '断言未通过：' + out.failedAssertions.join('；') }
      break
    case 'dns':
      if (out.last?.ok) metric = out.last.rtt_ms
      if (out.last && !out.last.ok) { failed = true; why = why || out.last.error || 'rcode ' + (out.last.rcode || '') }
      break
    case 'mtr':
      if (out.lastHop?.received) metric = out.lastHop.avg_ms
      if (r.status === 'done' && !out.reached) { failed = true; why = why || 'destination not reached' }
      break
  }
  out.metric = metric
  out.failed = failed
  out.finished = r.status === 'done' || r.status === 'error'
  out.stopped = r.status === 'done' && r.error === 'stopped'
  const cat = failed ? errorCategory(why) : null
  out.errorKind = cat?.key || ''
  out.errorText = failed ? why : ''
  // Timed out: the node got no answer in time, as opposed to being refused.
  out.timedOut = failed && ['timeout', 'connect_timeout', 'loss', 'noreply'].includes(out.errorKind)
  out.statusText = out.live ? (r.status === 'pending' ? '等待' : '进行中')
    : failed ? (task.type === 'mtr' && r.status === 'done' ? '未到达' : out.assertFail && out.last?.ok ? '断言未通过'
      : task.type === 'tls' && out.last?.ok ? (out.tls?.verified ? '即将到期' : '证书无效') : cat.label)
    : out.stopped ? '已停止' : '完成'
}
