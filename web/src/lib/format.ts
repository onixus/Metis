import type { Money } from '../api/types'
import { ru } from '../i18n/ru'

const dateFmt = new Intl.DateTimeFormat('ru-RU', { day: '2-digit', month: '2-digit', year: 'numeric' })
const dateTimeFmt = new Intl.DateTimeFormat('ru-RU', { dateStyle: 'short', timeStyle: 'short' })

export function fmtDate(value: string | null | undefined): string {
  if (!value) return ru.app.dash
  const d = new Date(value)
  return Number.isNaN(d.getTime()) ? value : dateFmt.format(d)
}

export function fmtDateTime(value: string | null | undefined): string {
  if (!value) return ru.app.dash
  const d = new Date(value)
  return Number.isNaN(d.getTime()) ? value : dateTimeFmt.format(d)
}

/** Деньги приходят в минорных единицах (int64) — переводим в основные без float-арифметики над суммой. */
export function fmtMoney(m: Money | null | undefined): string {
  if (!m) return ru.app.dash
  const neg = m.amount < 0
  const abs = Math.abs(m.amount)
  const major = Math.floor(abs / 100)
  const minor = abs % 100
  const majorStr = new Intl.NumberFormat('ru-RU').format(major)
  const minorStr = minor === 0 ? '' : `,${String(minor).padStart(2, '0')}`
  return `${neg ? '−' : ''}${majorStr}${minorStr} ${m.currency}`
}

/** Минорные единицы → значение для поля ввода в основных единицах (две цифры после запятой). */
export function formatMoneyInput(amount: number | null | undefined): string {
  if (amount === null || amount === undefined || !Number.isFinite(amount)) return ''
  const neg = amount < 0
  const abs = Math.abs(Math.trunc(amount))
  return `${neg ? '-' : ''}${Math.floor(abs / 100)}.${String(abs % 100).padStart(2, '0')}`
}

/** Значение поля ввода в основных единицах → минорные единицы; null, если пусто или не число. */
export function parseMoneyInput(value: string): number | null {
  const raw = value.trim().replace(',', '.')
  if (!raw) return null
  const n = Number(raw)
  if (!Number.isFinite(n)) return null
  return Math.round(n * 100)
}

/** Шаг поля ввода денег: одна минорная единица. */
export const MONEY_INPUT_STEP = 0.01

/** Разница в днях между двумя датами формата YYYY-MM-DD; null, если одной нет. */
export function daysBetween(a: string | null | undefined, b: string | null | undefined): number | null {
  if (!a || !b) return null
  const da = Date.parse(a)
  const db = Date.parse(b)
  if (Number.isNaN(da) || Number.isNaN(db)) return null
  return Math.round((db - da) / 86_400_000)
}

export function pick<T extends string>(map: Record<T, string>, key: string | undefined | null): string {
  if (!key) return ru.app.dash
  return (map as Record<string, string>)[key] ?? key
}
