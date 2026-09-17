package portfoliograph

import (
	"context"
	"sync"

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
	Load(ctx context.Context) (Snapshot, error)
	SaveProduct(ctx context.Context, p Product) error
	// DeleteProduct удаляет продукт вместе с его возможностями, фичами, требованиями и связями.
	DeleteProduct(ctx context.Context, id kernel.ID) error
	SaveCapability(ctx context.Context, c Capability) error
	SaveFeature(ctx context.Context, f Feature) error
	SaveRequirement(ctx context.Context, r Requirement) error
	SaveLink(ctx context.Context, l Link) error
	DeleteLink(ctx context.Context, id kernel.ID) error
	SaveContract(ctx context.Context, c IntegrationContract) error
	SaveSettings(ctx context.Context, s Settings) error
	SaveRollup(ctx context.Context, values []FeatureValue) error
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
func (m *MemStore) Load(context.Context) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.snap
	s.Products = append([]Product(nil), s.Products...)
	s.Capabilities = append([]Capability(nil), s.Capabilities...)
	s.Features = append([]Feature(nil), s.Features...)
	s.Requirements = append([]Requirement(nil), s.Requirements...)
	s.Links = append([]Link(nil), s.Links...)
	s.Contracts = append([]IntegrationContract(nil), s.Contracts...)
	return s, nil
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
func (m *MemStore) SaveProduct(_ context.Context, p Product) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snap.Products = upsert(m.snap.Products, p, func(a, b Product) bool { return a.ID == b.ID })
	return nil
}

// DeleteProduct удаляет продукт и всё, что ему принадлежит.
func (m *MemStore) DeleteProduct(_ context.Context, id kernel.ID) error {
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
func (m *MemStore) SaveCapability(_ context.Context, c Capability) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snap.Capabilities = upsert(m.snap.Capabilities, c, func(a, b Capability) bool { return a.ID == b.ID })
	return nil
}

// SaveFeature сохраняет фичу.
func (m *MemStore) SaveFeature(_ context.Context, f Feature) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snap.Features = upsert(m.snap.Features, f, func(a, b Feature) bool { return a.ID == b.ID })
	return nil
}

// SaveRequirement сохраняет требование.
func (m *MemStore) SaveRequirement(_ context.Context, r Requirement) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snap.Requirements = upsert(m.snap.Requirements, r, func(a, b Requirement) bool { return a.ID == b.ID })
	return nil
}

// SaveLink сохраняет связь.
func (m *MemStore) SaveLink(_ context.Context, l Link) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snap.Links = upsert(m.snap.Links, l, func(a, b Link) bool { return a.ID == b.ID })
	return nil
}

// DeleteLink удаляет связь.
func (m *MemStore) DeleteLink(_ context.Context, id kernel.ID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, l := range m.snap.Links {
		if l.ID == id {
			m.snap.Links = append(m.snap.Links[:i:i], m.snap.Links[i+1:]...)
			return nil
		}
	}
	return kernel.NotFound("link", id)
}

// SaveContract сохраняет контракт.
func (m *MemStore) SaveContract(_ context.Context, c IntegrationContract) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snap.Contracts = upsert(m.snap.Contracts, c, func(a, b IntegrationContract) bool { return a.ID == b.ID })
	return nil
}

// SaveSettings сохраняет настройки.
func (m *MemStore) SaveSettings(_ context.Context, s Settings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snap.Settings = s
	return nil
}

// SaveRollup сохраняет результат rollup.
func (m *MemStore) SaveRollup(_ context.Context, values []FeatureValue) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.roll = append([]FeatureValue(nil), values...)
	return nil
}

// Rollup возвращает последний сохранённый rollup (для тестов).
func (m *MemStore) Rollup() []FeatureValue {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]FeatureValue(nil), m.roll...)
}
