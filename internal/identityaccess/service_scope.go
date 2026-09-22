package identityaccess

import "github.com/onixus/metis/internal/identityaccess/authz"

// ServiceScope — область доступа сервисных компонентов (воркер, коннекторы):
// роль service, приватный доступ ко всем продуктам, внутренняя аудитория, без финансов.
func ServiceScope(name string) authz.Scope {
	return authz.New(authz.Params{
		Subject:     "service:" + name,
		Roles:       []authz.Role{authz.RoleService},
		AllProducts: authz.AccessPrivate,
		Audience:    authz.AudienceInternal,
	})
}

// FinanceServiceScope is reserved for explicitly configured financial imports.
// Ordinary connectors retain ServiceScope, which has no financial access.
func FinanceServiceScope(name string) authz.Scope {
	return authz.New(authz.Params{
		Subject:     "service:finance:" + name,
		Roles:       []authz.Role{authz.RoleService, authz.RoleFinance},
		AllProducts: authz.AccessPrivate,
		Audience:    authz.AudienceInternal,
		Finance:     authz.FinanceFull,
	})
}
