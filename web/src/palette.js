// Chart palette (validated with the dataviz palette checker for both surfaces).
// Categorical hues are assigned to agents in a fixed order (sorted agent id),
// never by rank, so a filter never repaints survivors.
export const categorical = {
  light: ['#2a78d6', '#eb6834', '#1baf7a', '#eda100', '#e87ba4', '#008300', '#4a3aa7', '#e34948'],
  dark: ['#3987e5', '#d95926', '#199e70', '#c98500', '#d55181', '#008300', '#9085e9', '#e66767'],
}
export const chrome = {
  light: { surface: '#ffffff', ink: '#0f172a', ink2: '#334155', muted: '#64748b', grid: '#e2e8f0', axis: '#cbd5e1' },
  dark: { surface: '#1e293b', ink: '#f8fafc', ink2: '#cbd5e1', muted: '#94a3b8', grid: '#334155', axis: '#475569' },
}
export const status = { good: '#15803d', warning: '#c2410c', serious: '#ea580c', critical: '#dc2626' }

export function isDark() {
  if (typeof document !== 'undefined') {
    const forced = document.documentElement.dataset.theme
    if (forced === 'dark') return true
    if (forced === 'light') return false
  }
  return false
}
export function seriesColor(i) {
  const list = categorical[isDark() ? 'dark' : 'light']
  return list[i % list.length]
}
