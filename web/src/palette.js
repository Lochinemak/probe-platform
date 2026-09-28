// Chart palette (validated with the dataviz palette checker for both surfaces).
// Categorical hues are assigned to agents in a fixed order (sorted agent id),
// never by rank, so a filter never repaints survivors.
export const categorical = {
  light: ['#2a78d6', '#eb6834', '#1baf7a', '#eda100', '#e87ba4', '#008300', '#4a3aa7', '#e34948'],
  dark: ['#3987e5', '#d95926', '#199e70', '#c98500', '#d55181', '#008300', '#9085e9', '#e66767'],
}
export const chrome = {
  light: { surface: '#fcfcfb', ink: '#0b0b0b', ink2: '#52514e', muted: '#898781', grid: '#e1e0d9', axis: '#c3c2b7' },
  dark: { surface: '#1a1a19', ink: '#ffffff', ink2: '#c3c2b7', muted: '#898781', grid: '#2c2c2a', axis: '#383835' },
}
export const status = { good: '#0ca30c', warning: '#fab219', serious: '#ec835a', critical: '#d03b3b' }

export function isDark() {
  return window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches
}
export function seriesColor(i) {
  const list = categorical[isDark() ? 'dark' : 'light']
  return list[i % list.length]
}
