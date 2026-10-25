export interface TabDef<K extends string> {
  key: K
  label: string
  disabled?: boolean
}

/** APEX tabs: one tab stop per group; arrows and Home/End select and focus a tab. */
export function Tabs<K extends string>({ tabs, value, onChange, label = 'Представление' }: { tabs: readonly TabDef<K>[]; value: K; onChange: (k: K) => void; label?: string }) {
  return (
    <div className="segmented" role="tablist" aria-label={label}>
      {tabs.map((t) => (
        <button
          key={t.key}
          type="button"
          role="tab"
          disabled={t.disabled}
          aria-selected={value === t.key}
          tabIndex={value === t.key ? 0 : -1}
          className={value === t.key ? 'active' : undefined}
          onClick={() => onChange(t.key)}
          onKeyDown={(event) => {
            if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
            event.preventDefault()
            const enabled = tabs.filter(tab => !tab.disabled)
            const index = enabled.findIndex(tab => tab.key === t.key)
            const next = event.key === 'Home' ? 0 : event.key === 'End' ? enabled.length - 1
              : (index + (event.key === 'ArrowRight' ? 1 : -1) + enabled.length) % enabled.length
            const target = enabled[next]
            if (!target) return
            onChange(target.key)
            const buttons = event.currentTarget.parentElement?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)')
            buttons?.[next]?.focus()
          }}
        >
          {t.label}
        </button>
      ))}
    </div>
  )
}
