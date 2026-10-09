package identityaccess

import (
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

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

// ModelCalculationScope is reserved for the internal aggregate evaluator. It
// preserves product visibility and grants no roles or write permissions. Raw
// rows must never escape the evaluator for an aggregates-only caller.
func ModelCalculationScope(caller authz.Scope) authz.Scope {
	if !caller.Valid() || caller.Finance() < authz.FinanceAggregates {
		return authz.Scope{}
	}
	products := make(map[kernel.ID]authz.Access)
	for _, id := range caller.ProductIDs(authz.AccessStrategic) {
		products[id] = caller.Product(id)
	}
	all := authz.AccessNone
	if caller.SeesAllProducts() {
		all = caller.Product(kernel.NilID)
	}
	return authz.New(authz.Params{Subject: caller.Subject(), Products: products, AllProducts: all, Audience: caller.Audience(), Finance: authz.FinanceFull})
}
