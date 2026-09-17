// Package authz определяет authz.Scope — область доступа субъекта.
// Scope создаётся только модулем identityaccess (forbidigo запрещает authz.New вне него).
// Нулевое значение запрещает всё (инвариант 3, NF-S01).
package authz

import (
	"context"

	"github.com/onixus/metis/internal/kernel"
)

// Role — роль пользователя (ТЗ 1.3).
type Role string

const (
	RoleCPO        Role = "cpo"
	RolePM         Role = "pm"
	RoleDevLead    Role = "dev_lead"
	RoleMarketing  Role = "marketing"
	RoleFinance    Role = "finance"
	RoleCompliance Role = "compliance"
	RolePresale    Role = "presale"
	RoleAdmin      Role = "admin"
	// RoleService — сервисная учётка коннекторов и воркера.
	RoleService Role = "service"
)

// Access — уровень доступа к продукту (ТЗ 2.4: приватный контур и стратегический срез).
type Access int

const (
	// AccessNone — продукт не виден.
	AccessNone Access = iota
	// AccessStrategic — виден стратегический срез: ценность, даты, обязательства, статус compliance.
	AccessStrategic
	// AccessPrivate — виден приватный контур: сырые сигналы, бэклог, discovery.
	AccessPrivate
)

// Audience — аудитория roadmap (RM-02).
type Audience string

const (
	AudienceInternal  Audience = "internal"
	AudienceSalesSafe Audience = "sales_safe"
)

// FinanceLevel — уровень доступа к финансовым данным (NF-S02).
type FinanceLevel int

const (
	FinanceNone FinanceLevel = iota
	FinanceAggregates
	FinanceFull
)

// Action — действие над ресурсом продукта.
type Action string

const (
	ActionReadStrategic  Action = "read_strategic"
	ActionReadPrivate    Action = "read_private"
	ActionWriteGraph     Action = "write_graph"
	ActionWritePriority  Action = "write_priority"
	ActionWriteRoadmap   Action = "write_roadmap"
	ActionWriteSignals   Action = "write_signals"
	ActionAdminSettings  Action = "admin_settings"
	ActionReadAudit      Action = "read_audit"
	ActionManageAccess   Action = "manage_access"
	ActionManageConnects Action = "manage_connectors"
)

// Scope — область доступа субъекта. Неизменяемый; нулевое значение запрещает всё.
type Scope struct {
	subject     string
	roles       map[Role]struct{}
	allProducts Access
	products    map[kernel.ID]Access
	audience    Audience
	finance     FinanceLevel
	initialized bool
}

// Params — параметры построения Scope; используются только identityaccess.
type Params struct {
	Subject     string
	Roles       []Role
	AllProducts Access
	Products    map[kernel.ID]Access
	Audience    Audience
	Finance     FinanceLevel
}

// New строит Scope. Вызов вне модуля identityaccess запрещён линтером forbidigo.
func New(p Params) Scope {
	s := Scope{
		subject:     p.Subject,
		roles:       make(map[Role]struct{}, len(p.Roles)),
		allProducts: p.AllProducts,
		products:    make(map[kernel.ID]Access, len(p.Products)),
		audience:    p.Audience,
		finance:     p.Finance,
		initialized: p.Subject != "",
	}
	for _, r := range p.Roles {
		s.roles[r] = struct{}{}
	}
	for id, a := range p.Products {
		s.products[id] = a
	}
	if s.audience == "" {
		s.audience = AudienceSalesSafe
	}
	return s
}

// Subject — идентификатор субъекта.
func (s Scope) Subject() string { return s.subject }

// Valid сообщает, построен ли Scope модулем identityaccess.
func (s Scope) Valid() bool { return s.initialized }

// HasRole проверяет роль.
func (s Scope) HasRole(r Role) bool {
	_, ok := s.roles[r]
	return ok
}

// Roles возвращает роли субъекта.
func (s Scope) Roles() []Role {
	out := make([]Role, 0, len(s.roles))
	for r := range s.roles {
		out = append(out, r)
	}
	return out
}

// Product возвращает уровень доступа к продукту.
func (s Scope) Product(id kernel.ID) Access {
	if !s.initialized {
		return AccessNone
	}
	a := s.products[id]
	if s.allProducts > a {
		a = s.allProducts
	}
	return a
}

// Audience — аудитория roadmap субъекта.
func (s Scope) Audience() Audience {
	if !s.initialized {
		return AudienceSalesSafe
	}
	return s.audience
}

// Finance — уровень доступа к финансам.
func (s Scope) Finance() FinanceLevel {
	if !s.initialized {
		return FinanceNone
	}
	return s.finance
}

// SeesAllProducts сообщает, распространяется ли доступ на все продукты (CPO, admin, presale — стратегически).
func (s Scope) SeesAllProducts() bool { return s.initialized && s.allProducts > AccessNone }

// ProductIDs возвращает продукты с явным доступом; для фильтра в репозиториях, когда !SeesAllProducts.
func (s Scope) ProductIDs(min Access) []kernel.ID {
	out := make([]kernel.ID, 0, len(s.products))
	for id, a := range s.products {
		if a >= min {
			out = append(out, id)
		}
	}
	return out
}

// Allows — решение политики для действия над продуктом (запрет по умолчанию).
func (s Scope) Allows(action Action, product kernel.ID) bool {
	if !s.initialized {
		return false
	}
	switch action {
	case ActionReadStrategic:
		return s.Product(product) >= AccessStrategic
	case ActionReadPrivate:
		return s.Product(product) >= AccessPrivate
	case ActionWriteGraph, ActionWritePriority, ActionWriteRoadmap:
		if s.HasRole(RoleAdmin) || s.HasRole(RoleCPO) || s.HasRole(RoleService) {
			return true
		}
		return s.HasRole(RolePM) && s.Product(product) >= AccessPrivate
	case ActionWriteSignals:
		if s.HasRole(RoleAdmin) || s.HasRole(RoleCPO) || s.HasRole(RoleService) {
			return true
		}
		return (s.HasRole(RolePM) || s.HasRole(RoleMarketing)) && s.Product(product) >= AccessPrivate
	case ActionAdminSettings, ActionManageAccess, ActionManageConnects:
		return s.HasRole(RoleAdmin)
	case ActionReadAudit:
		return s.HasRole(RoleAdmin) || s.HasRole(RoleCompliance)
	default:
		return false
	}
}

// Require возвращает ErrForbidden, если действие не разрешено.
func (s Scope) Require(action Action, product kernel.ID) error {
	if !s.Allows(action, product) {
		return kernel.ErrForbidden
	}
	return nil
}

type ctxKey struct{}

// WithScope кладёт Scope в контекст.
func WithScope(ctx context.Context, s Scope) context.Context {
	return context.WithValue(ctx, ctxKey{}, s)
}

// FromContext достаёт Scope из контекста; при отсутствии — нулевой (запрещает всё).
func FromContext(ctx context.Context) Scope {
	s, _ := ctx.Value(ctxKey{}).(Scope)
	return s
}
