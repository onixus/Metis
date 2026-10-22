import { useEffect, useState } from 'react'
import { Monitor, Moon, Sun } from 'lucide-react'
import { themeController } from '../theme-bootstrap'
import type { ThemePreference } from '../theme.js'

export function ThemeSwitch() {
  const [preference, setPreference] = useState(themeController.preference)
  useEffect(() => {
    const sync = () => setPreference(themeController.preference)
    window.addEventListener('storage', sync)
    return () => window.removeEventListener('storage', sync)
  }, [])
  const Icon = preference === 'system' ? Monitor : preference === 'dark' ? Moon : Sun
  return <label className="theme-switch"><Icon aria-hidden="true" />
    <span>Тема</span>
    <select aria-label="Тема оформления" value={preference} onChange={(event) => {
      const value = event.target.value as ThemePreference
      themeController.set(value)
      setPreference(value)
    }}>
      <option value="dark">Тёмная</option><option value="light">Светлая</option><option value="system">Как в системе</option>
    </select>
  </label>
}
