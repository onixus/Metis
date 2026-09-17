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

// FinanceServiceScope — область доступа сервисных задач, работающих с финансовыми данными
// (загрузка XLSX по расписанию, EC-01). Отличается от ServiceScope полным финансовым уровнем.
func FinanceServiceScope(name string) authz.Scope {
	return authz.New(authz.Params{
		Subject:     "service:" + name,
		Roles:       []authz.Role{authz.RoleService},
		AllProducts: authz.AccessPrivate,
		Audience:    authz.AudienceInternal,
		Finance:     authz.FinanceFull,
	})
}
