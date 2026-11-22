// The page lives in the URL path so a reload, a bookmark or the back button
// lands on the same view. There is no router library: parse() maps a path to a
// route object, navigate() changes it, and `route` is the reactive current one.
//
//   /                       probe page, last used probe type
//   /ping /tcping /http /udp /dns /mtr /tls /dns_compare /status
//                           probe page with that type (?target= prefills the form)
//   /t/<taskId>             probe page showing that task
//   /s/<token>              a shared result: public, read-only, no session needed
//   /coverage /monitors …    the other pages
import { reactive } from 'vue'

// Probe types in tab order. The id is both the task type and the URL path.
export const PROBE_TYPES = ['ping', 'tcping', 'http', 'udp', 'dns', 'mtr', 'tls', 'dns_compare', 'status']
// 节点状态 shows what the node's own machine is doing: administrators only.
export const ADMIN_PROBE_TYPES = ['status']
export const PAGES = ['coverage', 'monitors', 'agents', 'history', 'notify', 'settings']

export function parse(pathname, search = '') {
  const parts = String(pathname || '').split('/').filter(Boolean)
  const query = Object.fromEntries(new URLSearchParams(search))
  const [head, arg] = parts
  if (head === 't' && arg && parts.length === 2) return { page: 'probe', type: '', taskId: decodeURIComponent(arg), query }
  if (head === 's' && arg && parts.length === 2) return { page: 'share', type: '', taskId: '', token: decodeURIComponent(arg), query }
  if (parts.length === 1 && PROBE_TYPES.includes(head)) return { page: 'probe', type: head, taskId: '', query }
  if (parts.length === 1 && PAGES.includes(head)) return { page: head, type: '', taskId: '', query }
  // "/" and anything unknown: the probe page with whatever type was used last.
  return { page: 'probe', type: '', taskId: '', query }
}

export function taskPath(id) { return '/t/' + encodeURIComponent(id) }
export function sharePath(token) { return '/s/' + encodeURIComponent(token) }

const hasWindow = typeof window !== 'undefined'
export const route = reactive(hasWindow ? parse(location.pathname, location.search) : parse('/'))

function sync() { Object.assign(route, parse(location.pathname, location.search)) }

export function navigate(path, { replace = false } = {}) {
  if (path !== location.pathname + location.search) {
    window.history[replace ? 'replaceState' : 'pushState'](null, '', path)
  }
  sync()
}

if (hasWindow) window.addEventListener('popstate', sync)
