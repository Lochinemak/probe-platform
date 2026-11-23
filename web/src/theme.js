// Theme preference: 'light' (default, ilatency-style crisp emerald/slate),
// 'dark', or 'auto' (follow OS). Persisted in localStorage and synced via
// data-theme on <html> so CSS tokens and uPlot charts stay aligned.
import { ref } from 'vue'

const KEY = 'probe.theme'
const VALID = ['light', 'dark', 'auto']

function readStored() {
  try {
    const v = localStorage.getItem(KEY)
    return VALID.includes(v) ? v : 'light'
  } catch {
    return 'light'
  }
}

export const theme = ref(readStored())

export function resolvedTheme(mode = theme.value) {
  if (mode === 'dark' || mode === 'light') return mode
  if (typeof window !== 'undefined' && window.matchMedia) {
    return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
  }
  return 'light'
}

export function applyTheme(mode = theme.value) {
  if (typeof document === 'undefined') return
  const root = document.documentElement
  const actual = resolvedTheme(mode)
  root.dataset.theme = actual
  root.style.colorScheme = actual
  window.dispatchEvent(new CustomEvent('probe:theme', { detail: actual }))
}

export function setTheme(mode) {
  if (!VALID.includes(mode)) return
  theme.value = mode
  try { localStorage.setItem(KEY, mode) } catch { /* ignore */ }
  applyTheme(mode)
}

export function cycleTheme() {
  const order = ['light', 'dark', 'auto']
  setTheme(order[(order.indexOf(theme.value) + 1) % order.length])
}
