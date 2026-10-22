export type ThemePreference = 'dark' | 'light' | 'system'
export function readPreference(storage: Pick<Storage, 'getItem'>): ThemePreference
export function applyTheme(root: HTMLElement, preference: ThemePreference, systemDark: boolean): 'dark' | 'light'
export function startTheme(win: Window, root: HTMLElement): { readonly preference: ThemePreference; set(value: ThemePreference): void; dispose(): void }
