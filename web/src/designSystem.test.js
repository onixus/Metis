import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import { applyTheme, readPreference, startTheme } from './theme.js'

const root = () => ({ dataset: {}, style: {}, classList: { toggle(name, value) { this[name] = value } } })
test('DS56_prepaint_bootstrap_restores_all_preferences_and_denied_storage', () => {
  const html = readFileSync(new URL('../index.html', import.meta.url), 'utf8')
  const script = html.match(/<script>([\s\S]*?)<\/script>/)[1]
  for (const saved of [null, 'dark', 'light', 'system', 'invalid', 'denied']) {
    for (const systemDark of [true, false]) {
      const element = root()
      const storage = { getItem() { if (saved === 'denied') throw Error(); return saved } }
      runInNewContext(script, { document: { documentElement: element }, localStorage: storage, matchMedia: () => ({ matches: systemDark }) })
      const expected = saved === 'light' || saved === 'system' && !systemDark ? 'light' : 'dark'
      assert.equal(element.dataset.theme, expected)
      assert.equal(element.style.colorScheme, expected)
      assert.equal(element.classList.dark, expected === 'dark')
    }
  }
})
test('DS56_system_changes_cross_tab_reset_and_storage_failure', () => {
  let saved = 'system'
  const listeners = new Map()
  const media = { matches: true, addEventListener: (_, fn) => listeners.set('media', fn), removeEventListener: () => listeners.delete('media') }
  const storage = { getItem: () => saved, setItem: (_, value) => { saved = value } }
  const win = { localStorage: storage, matchMedia: () => media, addEventListener: (_, fn) => listeners.set('storage', fn), removeEventListener: () => listeners.delete('storage') }
  const element = root(), controller = startTheme(win, element)
  assert.equal(element.dataset.theme, 'dark')
  media.matches = false; listeners.get('media')(); assert.equal(element.dataset.theme, 'light')
  controller.set('dark'); media.matches = false; listeners.get('media')(); assert.equal(element.dataset.theme, 'dark')
  saved = 'light'; listeners.get('storage')({ key: 'apex.theme' }); assert.equal(controller.preference, 'light')
  saved = null; listeners.get('storage')({ key: null }); assert.equal(element.dataset.theme, 'dark')
  storage.setItem = () => { throw Error('denied') }; controller.set('light'); assert.equal(element.dataset.theme, 'light')
  controller.set('invalid'); assert.equal(element.dataset.theme, 'light')
  controller.dispose(); assert.equal(listeners.size, 0)
  assert.equal(readPreference({ getItem() { throw Error() } }), 'dark')
  assert.equal(applyTheme(root(), 'system', true), 'dark')
})

const tokens = readFileSync(new URL('./tokens.css', import.meta.url), 'utf8')
const blocks = [...tokens.matchAll(/(?:^|\n)(:root[^{}]*)\{([^{}]*)\}/g)]
const palette = (label) => Object.fromEntries([...blocks.find(([_, selector]) => selector.includes(label))[2].matchAll(/--([\w-]+):\s*(#[0-9a-f]{6});/g)].map(([, name, color]) => [name, color]))
function luminance(hex) {
  const rgb = hex.slice(1).match(/../g).map(x => parseInt(x, 16) / 255).map(x => x <= .04045 ? x / 12.92 : ((x + .055) / 1.055) ** 2.4)
  return rgb[0] * .2126 + rgb[1] * .7152 + rgb[2] * .0722
}
function contrast(a, b) { const x = luminance(a), y = luminance(b); return (Math.max(x, y) + .05) / (Math.min(x, y) + .05) }
test('DS56_text_status_severity_and_control_contrast_in_both_themes', () => {
  for (const theme of ['dark', 'light']) {
    const t = palette(`data-theme="${theme}"`)
    for (const bg of ['bg-canvas', 'surface-1', 'surface-2']) {
      for (const ink of ['text-primary', 'text-secondary', 'text-muted', 'accent', 'success', 'warning', 'danger', 'sev-critical', 'sev-high', 'sev-medium', 'sev-low', 'sev-info']) {
        assert.ok(contrast(t[ink], t[bg]) >= 4.5, `${theme} ${ink}/${bg}: ${contrast(t[ink], t[bg])}`)
      }
      assert.ok(contrast(t['border-control'], t[bg]) >= 3, `${theme} border/${bg}`)
      assert.ok(contrast(t['focus-ring'], t[bg]) >= 3, `${theme} focus/${bg}`)
    }
    assert.ok(contrast(t.accent, t['on-accent']) >= 4.5)
  }
})
function files(dir) { return readdirSync(dir, { withFileTypes: true }).flatMap(entry => entry.isDirectory() ? files(`${dir}/${entry.name}`) : [`${dir}/${entry.name}`]) }
test('DS56_no_color_literals_raw_neutrals_or_remote_fonts_outside_tokens', () => {
  const src = fileURLToPath(new URL('.', import.meta.url))
  for (const file of files(src).filter(f => /\.(tsx|css)$/.test(f) && !f.endsWith('/tokens.css'))) {
    const text = readFileSync(file, 'utf8')
    assert.doesNotMatch(text, /#[0-9a-f]{3,8}\b|\b(?:rgb|rgba|hsl|hsla)\s*\(|\b(?:slate|gray)-\d|fonts\.google/i, file)
    assert.doesNotMatch(text, /font-size:\s*(?:[0-9]|10)px/, file)
  }
})
