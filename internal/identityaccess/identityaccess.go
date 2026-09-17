// Package identityaccess — аутентификация по OIDC (AD-01) и построение authz.Scope (AD-02).
//
// Роли берутся из клейма roles токена, продукты приватного контура — из клейма products
// (ключи продуктов). Стратегический срез связанных продуктов вычисляется по рёбрам графа
// через порт GraphNeighbors (PG-10).
package identityaccess

import (
	"context"
	"fmt"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// Claims — проверенные клеймы токена, независимые от провайдера.
type Claims struct {
	Subject  string
	Roles    []string
	Products []string // ключи продуктов приватного контура
	Finance  string   // "", "aggregates", "full"
}

// TokenVerifier проверяет токен и возвращает клеймы.
type TokenVerifier interface {
	Verify(ctx context.Context, rawToken string) (Claims, error)
}

// ProductDirectory — порт: ключ продукта → идентификатор; реализуется portfoliograph.
type ProductDirectory interface {
	ProductIDByKey(ctx context.Context, key string) (kernel.ID, error)
}

// GraphNeighbors — порт: продукты, связанные ребром графа с данным; реализуется portfoliograph.
type GraphNeighbors interface {
	LinkedProducts(ctx context.Context, productID kernel.ID) ([]kernel.ID, error)
}

// Resolver строит Scope из клеймов.
type Resolver struct {
	dir   ProductDirectory
	graph GraphNeighbors
}

// NewResolver создаёт Resolver.
func NewResolver(dir ProductDirectory, graph GraphNeighbors) *Resolver {
	return &Resolver{dir: dir, graph: graph}
}

// ScopeFor строит область доступа для клеймов. Неизвестные роли и продукты игнорируются.
func (r *Resolver) ScopeFor(ctx context.Context, c Claims) (authz.Scope, error) {
	if c.Subject == "" {
		return authz.Scope{}, fmt.Errorf("%w: пустой subject", kernel.ErrForbidden)
	}
	p := authz.Params{
		Subject:  c.Subject,
		Products: map[kernel.ID]authz.Access{},
		Audience: authz.AudienceInternal,
	}
	for _, raw := range c.Roles {
		role, ok := knownRole(raw)
		if !ok {
			continue
		}
		p.Roles = append(p.Roles, role)
		switch role {
		case authz.RoleCPO, authz.RoleAdmin, authz.RoleDevLead, authz.RoleCompliance, authz.RoleMarketing, authz.RoleFinance, authz.RoleService:
			p.AllProducts = authz.AccessPrivate
		case authz.RolePresale:
			if p.AllProducts < authz.AccessStrategic {
				p.AllProducts = authz.AccessStrategic
			}
		case authz.RolePM:
		}
	}
	if len(p.Roles) == 0 {
		return authz.Scope{}, fmt.Errorf("%w: нет известных ролей", kernel.ErrForbidden)
	}
	onlyPresale := len(p.Roles) == 1 && p.Roles[0] == authz.RolePresale
	if onlyPresale {
		p.Audience = authz.AudienceSalesSafe
	}
	switch c.Finance {
	case "full":
		p.Finance = authz.FinanceFull
	case "aggregates":
		p.Finance = authz.FinanceAggregates
	}
	for _, key := range c.Products {
		id, err := r.dir.ProductIDByKey(ctx, key)
		if err != nil {
			if isNotFound(err) {
				continue
			}
			return authz.Scope{}, fmt.Errorf("продукт %q: %w", key, err)
		}
		p.Products[id] = authz.AccessPrivate
		neighbors, err := r.graph.LinkedProducts(ctx, id)
		if err != nil {
			return authz.Scope{}, fmt.Errorf("соседи продукта %q: %w", key, err)
		}
		for _, n := range neighbors {
			if p.Products[n] < authz.AccessStrategic {
				p.Products[n] = authz.AccessStrategic
			}
		}
	}
	return authz.New(p), nil
}

func knownRole(raw string) (authz.Role, bool) {
	switch authz.Role(raw) {
	case authz.RoleCPO, authz.RolePM, authz.RoleDevLead, authz.RoleMarketing, authz.RoleFinance,
		authz.RoleCompliance, authz.RolePresale, authz.RoleAdmin, authz.RoleService:
		return authz.Role(raw), true
	}
	return "", false
}

func isNotFound(err error) bool { return err != nil && errorsIs(err, kernel.ErrNotFound) }
