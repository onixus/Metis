const ADMIN_ROLES = new Set(['admin', 'compliance'])

/** Раздел администрирования доступен ролям admin и compliance. */
export function isAdmin(roles: readonly string[] | undefined): boolean {
  return (roles ?? []).some((r) => ADMIN_ROLES.has(r))
}
