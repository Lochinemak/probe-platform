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
  login: (password) => request('POST', '/api/login', { password }),
  logout: () => request('POST', '/api/logout'),
  agents: () => request('GET', '/api/agents'),
  deleteAgent: (id) => request('DELETE', `/api/agents/${encodeURIComponent(id)}`),
  tasks: (limit = 50, offset = 0, monitor = '') => request('GET', `/api/tasks?limit=${limit}&offset=${offset}&monitor=${encodeURIComponent(monitor)}`),
  task: (id) => request('GET', `/api/tasks/${encodeURIComponent(id)}`),
  createTask: (body) => request('POST', '/api/tasks', body),
  cancelTask: (id) => request('POST', `/api/tasks/${encodeURIComponent(id)}/cancel`),
  agentFiles: () => request('GET', '/api/agent/version'),
  agentToken: () => request('GET', '/api/agent/token'),
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
}

// subscribe opens the SSE stream for a task. Returns a function that closes it.
export function subscribe(taskId, onEvent, onError) {
  const es = new EventSource(`/api/tasks/${encodeURIComponent(taskId)}/events`)
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
