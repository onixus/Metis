import type { AccessLevel, Me } from '../api/types'

const ADMIN_ROLES = new Set(['admin', 'compliance'])

/** Раздел администрирования доступен ролям admin и compliance. */
export function isAdmin(roles: readonly string[] | undefined): boolean {
  return (roles ?? []).some((r) => ADMIN_ROLES.has(r))
}

export function hasRole(me: Me | undefined, ...roles: string[]): boolean {
  const mine = me?.roles ?? []
  return roles.some((r) => mine.includes(r))
}

/** Политики записи повторяют authz.Scope (уровень доступа к продукту — из /me). */
export function canWriteDiscovery(me: Me | undefined, level: AccessLevel): boolean {
  return hasRole(me, 'admin', 'cpo') || (hasRole(me, 'pm', 'marketing') && level === 'private')
}

export function canWriteCommitments(me: Me | undefined, level: AccessLevel): boolean {
  return hasRole(me, 'admin', 'cpo') || (hasRole(me, 'pm', 'compliance') && level === 'private')
}

export function canWriteCompliance(me: Me | undefined, level: AccessLevel): boolean {
  return hasRole(me, 'admin') || (hasRole(me, 'compliance') && level !== 'none')
}

export function canWriteDecisions(me: Me | undefined, level: AccessLevel): boolean {
  return hasRole(me, 'admin', 'cpo') || (hasRole(me, 'pm') && level === 'private')
}

export function canWriteRoadmap(me: Me | undefined, level: AccessLevel): boolean {
  return hasRole(me, 'admin', 'cpo') || (hasRole(me, 'pm') && level === 'private')
}

/** Портфельный список решений (без продукта) читают только cpo, admin и service — см. internal/decisions/service.go. */
export function canReadPortfolioDecisions(me: Me | undefined): boolean {
  return hasRole(me, 'cpo', 'admin', 'service')
}

/** Compliance-дашборд: руководитель РБПО, CPO и администратор. */
export function canSeeCompliance(me: Me | undefined): boolean {
  return hasRole(me, 'compliance', 'cpo', 'admin')
}
