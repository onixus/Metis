package portfoliograph

import (
	"context"
	"sync"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// Snapshot — полное содержимое графа для загрузки в память.
type Snapshot struct {
	Products     []Product
	Capabilities []Capability
	Features     []Feature
	Requirements []Requirement
	Links        []Link
	Contracts    []IntegrationContract
	Settings     Settings
}

// Store — хранилище графа. Реализации: память (тесты, стенд), PostgreSQL (internal/pg).
// Все методы принимают контекст; авторизация выполняется в Service до вызова хранилища.
type Store interface {
	Load(ctx context.Context, sc authz.Scope) (Snapshot, error)
	SaveProduct(ctx context.Context, sc authz.Scope, p Product) error
	// DeleteProduct удаляет продукт вместе с его возможностями, фичами, требованиями и связями.
	DeleteProduct(ctx context.Context, sc authz.Scope, id kernel.ID) error
	SaveCapability(ctx context.Context, sc authz.Scope, c Capability) error
	SaveFeature(ctx context.Context, sc authz.Scope, f Feature) error
	SaveRequirement(ctx context.Context, sc authz.Scope, r Requirement) error
	SaveLink(ctx context.Context, sc authz.Scope, l Link) error
	DeleteLink(ctx context.Context, sc authz.Scope, id kernel.ID) error
	SaveContract(ctx context.Context, sc authz.Scope, c IntegrationContract) error
	SaveSettings(ctx context.Context, sc authz.Scope, s Settings) error
	SaveRollup(ctx context.Context, sc authz.Scope, values []FeatureValue) error
}

// MemStore — хранилище в памяти.
type MemStore struct {
	mu   sync.Mutex
	snap Snapshot
	roll []FeatureValue
}

// NewMemStore создаёт пустое хранилище.
func NewMemStore() *MemStore { return &MemStore{snap: Snapshot{Settings: DefaultSettings()}} }

// Load возвращает копию снимка.
func (m *MemStore) Load(_ context.Context, sc authz.Scope) (Snapshot, error) {
	if err := RequireSnapshot(sc); err != nil {
		return Snapshot{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.snap
	s.Products = append([]Product(nil), s.Products...)
	s.Capabilities = append([]Capability(nil), s.Capabilities...)
	s.Features = append([]Feature(nil), s.Features...)
	s.Requirements = append([]Requirement(nil), s.Requirements...)
	s.Links = append([]Link(nil), s.Links...)
	s.Contracts = append([]IntegrationContract(nil), s.Contracts...)
	return kernel.CloneValue(s), nil
}

func upsert[T any](s []T, v T, same func(a, b T) bool) []T {
	for i := range s {
		if same(s[i], v) {
			s[i] = v
			return s
		}
	}
	return append(s, v)
}

// SaveProduct сохраняет продукт.
func (m *MemStore) SaveProduct(_ context.Context, sc authz.Scope, p Product) error {
	p = kernel.CloneValue(p)
	if err := RequireProductWrite(sc, p.ID); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snap.Products = upsert(m.snap.Products, p, func(a, b Product) bool { return a.ID == b.ID })
	return nil
}

// DeleteProduct удаляет продукт и всё, что ему принадлежит.
func (m *MemStore) DeleteProduct(_ context.Context, sc authz.Scope, id kernel.ID) error {
	if err := RequireProductWrite(sc, id); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	found := false
	m.snap.Products = filter(m.snap.Products, func(p Product) bool { found = found || p.ID == id; return p.ID != id })
	if !found {
		return kernel.NotFound("product", id)
	}
	m.snap.Capabilities = filter(m.snap.Capabilities, func(c Capability) bool { return c.ProductID != id })
	m.snap.Features = filter(m.snap.Features, func(f Feature) bool { return f.ProductID != id })
	m.snap.Requirements = filter(m.snap.Requirements, func(r Requirement) bool { return r.ProductID != id })
	m.snap.Links = filter(m.snap.Links, func(l Link) bool { return l.FromProductID != id && l.ToProductID != id })
	m.roll = filter(m.roll, func(v FeatureValue) bool { return v.ProductID != id })
	return nil
}

func filter[T any](s []T, keep func(T) bool) []T {
	out := s[:0:0]
	for _, v := range s {
		if keep(v) {
			out = append(out, v)
		}
	}
	return out
}

// SaveCapability сохраняет возможность.
func (m *MemStore) SaveCapability(_ context.Context, sc authz.Scope, c Capability) error {
	c = kernel.CloneValue(c)
	if err := RequireProductWrite(sc, c.ProductID); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, old := range m.snap.Capabilities {
		if old.ID == c.ID && (old.ProductID != c.ProductID) {
			return kernel.ErrForbidden
		}
	}
	m.snap.Capabilities = upsert(m.snap.Capabilities, c, func(a, b Capability) bool { return a.ID == b.ID })
	return nil
}

// SaveFeature сохраняет фичу.
func (m *MemStore) SaveFeature(_ context.Context, sc authz.Scope, f Feature) error {
	f = kernel.CloneValue(f)
	if err := RequireProductWrite(sc, f.ProductID); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, old := range m.snap.Features {
		if old.ID == f.ID && (old.ProductID != f.ProductID) {
			return kernel.ErrForbidden
		}
	}
	m.snap.Features = upsert(m.snap.Features, f, func(a, b Feature) bool { return a.ID == b.ID })
	return nil
}

// SaveRequirement сохраняет требование.
func (m *MemStore) SaveRequirement(_ context.Context, sc authz.Scope, r Requirement) error {
	r = kernel.CloneValue(r)
	if err := RequireProductWrite(sc, r.ProductID); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, old := range m.snap.Requirements {
		if old.ID == r.ID && (old.ProductID != r.ProductID) {
			return kernel.ErrForbidden
		}
	}
	m.snap.Requirements = upsert(m.snap.Requirements, r, func(a, b Requirement) bool { return a.ID == b.ID })
	return nil
}

// SaveLink сохраняет связь.
func (m *MemStore) SaveLink(_ context.Context, sc authz.Scope, l Link) error {
	l = kernel.CloneValue(l)
	if err := RequireLinkWrite(sc, l.FromProductID, l.ToProductID); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, old := range m.snap.Links {
		if old.ID == l.ID && (old.FromProductID != l.FromProductID || old.ToProductID != l.ToProductID) {
			return kernel.ErrForbidden
		}
	}
	m.snap.Links = upsert(m.snap.Links, l, func(a, b Link) bool { return a.ID == b.ID })
	return nil
}

// DeleteLink удаляет связь.
func (m *MemStore) DeleteLink(_ context.Context, sc authz.Scope, id kernel.ID) error {
	if !sc.Valid() {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, l := range m.snap.Links {
		if l.ID == id {
			if err := RequireLinkWrite(sc, l.FromProductID, l.ToProductID); err != nil {
				return err
			}
			m.snap.Links = append(m.snap.Links[:i:i], m.snap.Links[i+1:]...)
			return nil
		}
	}
	return kernel.NotFound("link", id)
}

// SaveContract сохраняет контракт.
func (m *MemStore) SaveContract(_ context.Context, sc authz.Scope, c IntegrationContract) error {
	c = kernel.CloneValue(c)
	if err := RequireLinkWrite(sc, c.ProviderProductID, c.ConsumerProductID); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, old := range m.snap.Contracts {
		if old.ID == c.ID && (old.ProviderProductID != c.ProviderProductID || old.ConsumerProductID != c.ConsumerProductID) {
			return kernel.ErrForbidden
		}
	}
	m.snap.Contracts = upsert(m.snap.Contracts, c, func(a, b IntegrationContract) bool { return a.ID == b.ID })
	return nil
}

// SaveSettings сохраняет настройки.
func (m *MemStore) SaveSettings(_ context.Context, sc authz.Scope, s Settings) error {
	s = kernel.CloneValue(s)
	if err := sc.Require(authz.ActionAdminSettings, kernel.NilID); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snap.Settings = s
	return nil
}

// SaveRollup сохраняет результат rollup.
func (m *MemStore) SaveRollup(_ context.Context, sc authz.Scope, values []FeatureValue) error {
	values = kernel.CloneValue(values)
	if err := RequireSnapshot(sc); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.roll = append([]FeatureValue(nil), values...)
	return nil
}

// Rollup возвращает последний сохранённый rollup (для тестов).
func (m *MemStore) Rollup(_ context.Context, sc authz.Scope) ([]FeatureValue, error) {
	if err := RequireSnapshot(sc); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return kernel.CloneValue(append([]FeatureValue(nil), m.roll...)), nil
}
