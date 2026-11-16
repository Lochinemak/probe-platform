// Recently used targets, kept per probe type in localStorage so the target
// box can offer them back.
const MAX = 12
const key = (type) => `probe.recent.${type}`
const LEGACY_KEY = 'probe.recent'

function read(k) {
  try {
    const v = JSON.parse(localStorage.getItem(k) || '[]')
    return Array.isArray(v) ? v.filter((x) => typeof x === 'string' && x) : []
  } catch { return [] }
}
function write(k, list) {
  try {
    if (list.length) localStorage.setItem(k, JSON.stringify(list))
    else localStorage.removeItem(k)
  } catch { /* storage full or disabled: history is a convenience */ }
}

// The first releases kept one list for every type. Sort its entries by what
// they look like, once, and drop the old key.
export function migrateLegacy() {
  const old = read(LEGACY_KEY)
  try { localStorage.removeItem(LEGACY_KEY) } catch { /* ignore */ }
  for (const t of old.reverse()) add(guessType(t), t)
}
export function guessType(target) {
  if (/^https?:\/\//i.test(target)) return 'http'
  if (/^[^:[\]]+:\d+$/.test(target) || /^\[.*\]:\d+$/.test(target)) return 'tcping'
  return 'ping'
}

export function list(type) { return read(key(type)) }

export function add(type, target) {
  const next = [target, ...list(type).filter((x) => x !== target)].slice(0, MAX)
  write(key(type), next)
  return next
}

export function remove(type, target) {
  const next = list(type).filter((x) => x !== target)
  write(key(type), next)
  return next
}

export function clear(type) {
  write(key(type), [])
  return []
}
