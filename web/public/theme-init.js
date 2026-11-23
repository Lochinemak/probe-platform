// Applies the saved theme before the page paints, so a dark-mode visitor does
// not see a flash of the light theme. A separate file rather than an inline
// script: the Content-Security-Policy allows no inline scripts. Keep the
// storage key and values in sync with src/theme.js.
try {
  var m = localStorage.getItem('probe.theme') || 'light'
  var t = m === 'auto' ? (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light') : (m === 'dark' ? 'dark' : 'light')
  document.documentElement.dataset.theme = t
  document.documentElement.style.colorScheme = t
} catch (e) {}
