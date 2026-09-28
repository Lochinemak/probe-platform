// Turns a raw AgentResult (final data or streamed progress) into what the
// tables render, so live and historical views share one code path.

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
  const resolvedIP = progress.find((p) => p.kind === 'resolved')?.data || ''
  switch (task.type) {
    case 'ping':
    case 'tcping': {
      let replies = data?.replies
      if (!replies) replies = progress.filter((p) => p.kind === 'reply').map((p) => p.data)
      replies = [...replies].sort((a, b) => a.seq - b.seq)
      out.replies = replies
      out.stats = data?.stats || statsOf(replies)
      out.ip = data?.ip || resolvedIP
      out.port = data?.port
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
      out.avg = {}
      for (const k of phases) out.avg[k] = okA.length ? okA.reduce((s, a) => s + (a.timing?.[k] || 0), 0) / okA.length : undefined
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
      break
    }
  }
  return out
}
