export interface TabDef<K extends string> {
  key: K
  label: string
}

/** Сегментированный переключатель вкладок (как в RoadmapPage). */
export function Tabs<K extends string>({ tabs, value, onChange }: { tabs: readonly TabDef<K>[]; value: K; onChange: (k: K) => void }) {
  return (
    <div className="segmented" role="tablist">
      {tabs.map((t) => (
        <button
          key={t.key}
          type="button"
          role="tab"
          aria-selected={value === t.key}
          className={value === t.key ? 'active' : undefined}
          onClick={() => onChange(t.key)}
        >
          {t.label}
        </button>
      ))}
    </div>
  )
}
