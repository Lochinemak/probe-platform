// Thin fetch wrapper. A 401 anywhere dispatches `probe:unauthorized` so the
// app can show the login screen.
async function request(method, url, body) {
  const opts = { method, credentials: 'same-origin', headers: {} }
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json'
    opts.body = JSON.stringify(body)
  }
  const res = await fetch(url, opts)
  let data = {}
  try { data = await res.json() } catch { /* empty body */ }
  if (res.status === 401) {
    window.dispatchEvent(new CustomEvent('probe:unauthorized'))
    throw Object.assign(new Error(data.error || '未登录'), { status: 401 })
  }
  if (!res.ok) throw Object.assign(new Error(data.error || res.statusText), { status: res.status })
  return data
}

export const api = {
  session: () => request('GET', '/api/session'),
  login: (username, password) => request('POST', '/api/login', { username, password }),
  logout: () => request('POST', '/api/logout'),
  agents: () => request('GET', '/api/agents'),
  createAgent: (body) => request('POST', '/api/agents', body),
  deleteAgent: (id) => request('DELETE', `/api/agents/${encodeURIComponent(id)}`),
  patchAgent: (id, body) => request('PATCH', `/api/agents/${encodeURIComponent(id)}`, body),
  stats: () => request('GET', '/api/stats'),
  resolvers: () => request('GET', '/api/dns/resolvers'),
  ipMe: () => request('GET', '/api/ip/me'),
  ipLookup: (ip) => request('GET', `/api/ip/lookup?ip=${encodeURIComponent(ip)}`),
  agentToken: (id) => request('GET', `/api/agents/${encodeURIComponent(id)}/token`),
  resetAgentToken: (id) => request('POST', `/api/agents/${encodeURIComponent(id)}/token`),
  tasks: (limit = 50, offset = 0, monitor = '') => request('GET', `/api/tasks?limit=${limit}&offset=${offset}&monitor=${encodeURIComponent(monitor)}`),
  task: (id) => request('GET', `/api/tasks/${encodeURIComponent(id)}`),
  createTask: (body) => request('POST', '/api/tasks', body),
  cancelTask: (id) => request('POST', `/api/tasks/${encodeURIComponent(id)}/cancel`),
  stopTask: (id) => request('POST', `/api/tasks/${encodeURIComponent(id)}/stop`),
  shares: (taskId) => request('GET', `/api/tasks/${encodeURIComponent(taskId)}/shares`),
  createShare: (taskId, body) => request('POST', `/api/tasks/${encodeURIComponent(taskId)}/shares`, body),
  revokeShare: (token) => request('DELETE', `/api/shares/${encodeURIComponent(token)}`),
  share: (token) => request('GET', `/api/shares/${encodeURIComponent(token)}`),
  agentFiles: () => request('GET', '/api/agent/version'),
  monitors: () => request('GET', '/api/monitors'),
  monitor: (id) => request('GET', `/api/monitors/${encodeURIComponent(id)}`),
  createMonitor: (body) => request('POST', '/api/monitors', body),
  updateMonitor: (id, body) => request('PUT', `/api/monitors/${encodeURIComponent(id)}`, body),
  deleteMonitor: (id) => request('DELETE', `/api/monitors/${encodeURIComponent(id)}`),
  runMonitor: (id) => request('POST', `/api/monitors/${encodeURIComponent(id)}/run`),
  monitorSeries: (id, range) => request('GET', `/api/monitors/${encodeURIComponent(id)}/series?range=${range}`),
  monitorAlerts: (id, limit = 50) => request('GET', `/api/monitors/${encodeURIComponent(id)}/alerts?limit=${limit}`),
  alerts: (limit = 50) => request('GET', `/api/alerts?limit=${limit}`),
  channels: () => request('GET', '/api/notify'),
  createChannel: (body) => request('POST', '/api/notify', body),
  updateChannel: (id, body) => request('PUT', `/api/notify/${encodeURIComponent(id)}`, body),
  deleteChannel: (id) => request('DELETE', `/api/notify/${encodeURIComponent(id)}`),
  testChannel: (id) => request('POST', `/api/notify/${encodeURIComponent(id)}/test`),
  settings: () => request('GET', '/api/settings'),
  updateSettings: (patch) => request('PUT', '/api/settings', patch),
  changePassword: (current, next) => request('POST', '/api/settings/password', { current, new: next }),
  testLogto: () => request('POST', '/api/settings/logto/test', {}),
}

// subscribe opens the SSE stream for a task. Returns a function that closes it.
export function subscribe(taskId, onEvent, onError) {
  return stream(`/api/tasks/${encodeURIComponent(taskId)}/events`, onEvent, onError)
}
// subscribeShare is the same stream for a shared result that is still running.
export function subscribeShare(token, onEvent, onError) {
  return stream(`/api/shares/${encodeURIComponent(token)}/events`, onEvent, onError)
}
function stream(url, onEvent, onError) {
  const es = new EventSource(url)
  for (const type of ['snapshot', 'progress', 'result', 'done']) {
    es.addEventListener(type, (e) => {
      let data = {}
      try { data = JSON.parse(e.data) } catch { /* ignore */ }
      onEvent(type, data)
      if (type === 'done') es.close()
    })
  }
  es.onerror = () => {
    if (es.readyState === EventSource.CLOSED && onError) onError(new Error('连接已断开'))
  }
  return () => es.close()
}

// propagation runs a global DNS propagation check and feeds its events
// (start, stage, row, done) to onEvent. It is read with fetch rather than
// EventSource so that a refusal (bad name, rate limit) arrives with the
// server's message. Returns a function that abandons the run.
export function propagation(params, onEvent, onError) {
  const ctl = new AbortController()
  const qs = new URLSearchParams(Object.entries(params).filter(([, v]) => v !== '' && v != null)).toString()
  ;(async () => {
    try {
      const res = await fetch('/api/dns/propagation?' + qs, { credentials: 'same-origin', signal: ctl.signal, headers: { Accept: 'text/event-stream' } })
      if (!res.ok) {
        let data = {}
        try { data = await res.json() } catch { /* not JSON */ }
        if (res.status === 401) window.dispatchEvent(new CustomEvent('probe:unauthorized'))
        throw Object.assign(new Error(data.error || res.statusText), { status: res.status })
      }
      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buf = ''
      let finished = false
      for (;;) {
        const { value, done } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        let cut
        while ((cut = buf.indexOf('\n\n')) >= 0) {
          const ev = parseSSE(buf.slice(0, cut))
          buf = buf.slice(cut + 2)
          if (!ev) continue
          finished = finished || ev.type === 'done'
          onEvent(ev.type, ev.data)
        }
      }
      if (!finished) throw new Error('连接中断，检测没有完成')
    } catch (e) {
      if (e.name !== 'AbortError' && onError) onError(e)
    }
  })()
  return () => ctl.abort()
}
// parseSSE reads one "event: …\ndata: …" block.
export function parseSSE(block) {
  let type = 'message'
  const data = []
  for (const line of block.split('\n')) {
    if (line.startsWith('event:')) type = line.slice(6).trim()
    else if (line.startsWith('data:')) data.push(line.slice(5).replace(/^ /, ''))
  }
  if (!data.length) return null
  try { return { type, data: JSON.parse(data.join('\n')) } } catch { return null }
}
