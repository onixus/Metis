// Shared by the pre-paint bootstrap and React. No persistent data other than theme.
export function readPreference(storage) {
  try { const value = storage.getItem('apex.theme'); return ['dark', 'light', 'system'].includes(value) ? value : 'dark' }
  catch { return 'dark' }
}
export function applyTheme(root, preference, systemDark) {
  const theme = preference === 'system' ? (systemDark ? 'dark' : 'light') : preference
  root.dataset.theme = theme
  root.classList.toggle('dark', theme === 'dark')
  root.style.colorScheme = theme
  return theme
}
export function startTheme(win, root) {
  const media = win.matchMedia('(prefers-color-scheme: dark)')
  let preference
  try { preference = readPreference(win.localStorage) } catch { preference = 'dark' }
  const sync = () => applyTheme(root, preference, media.matches)
  const storage = (event) => {
    if (event.key === 'apex.theme' || event.key === null) {
      try { preference = readPreference(win.localStorage) } catch { preference = 'dark' }
      sync()
    }
  }
  media.addEventListener('change', sync)
  win.addEventListener('storage', storage)
  sync()
  return {
    get preference() { return preference },
    set(value) {
      if (!['dark', 'light', 'system'].includes(value)) return
      preference = value
      try { win.localStorage.setItem('apex.theme', value) } catch { /* Keep in-memory preference. */ }
      sync()
    },
    dispose() { media.removeEventListener('change', sync); win.removeEventListener('storage', storage) },
  }
}
