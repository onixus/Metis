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
