package delivery

import (
	"context"
	"sort"
	"sync"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// Store — хранилище проекции трекера. Реализация на PostgreSQL — в internal/ (итерация db).
type Store interface {
	SaveMapping(ctx context.Context, sc authz.Scope, m Mapping) error
	MappingByFeature(ctx context.Context, sc authz.Scope, featureID kernel.ID) (Mapping, error)
	MappingByEpic(ctx context.Context, sc authz.Scope, epicKey string) (Mapping, error)
	Mappings(ctx context.Context, sc authz.Scope) ([]Mapping, error)
	SaveReleaseMapping(ctx context.Context, sc authz.Scope, m ReleaseMapping) error
	ReleaseMappings(ctx context.Context, sc authz.Scope) ([]ReleaseMapping, error)

	SaveEpic(ctx context.Context, sc authz.Scope, p EpicProjection) error
	EpicByFeature(ctx context.Context, sc authz.Scope, featureID kernel.ID) (EpicProjection, error)
	SaveSprints(ctx context.Context, sc authz.Scope, productID kernel.ID, sprints []SprintStatus) error
	Sprints(ctx context.Context, sc authz.Scope, productID kernel.ID) ([]SprintStatus, error)

	SaveSyncState(ctx context.Context, sc authz.Scope, s SyncState) error
	SyncState(ctx context.Context, sc authz.Scope) (SyncState, error)
	SaveFieldMapping(ctx context.Context, sc authz.Scope, m FieldMapping) error
	FieldMapping(ctx context.Context, sc authz.Scope) (FieldMapping, error)

	// MarkProcessed запоминает внешний ключ события; возвращает false, если событие уже обработано (ТЗ 4.2).
	MarkProcessed(ctx context.Context, sc authz.Scope, externalID string) (bool, error)
}

// MemStore — хранилище в памяти для тестов и локального запуска.
type MemStore struct {
	mu        sync.RWMutex
	mappings  map[kernel.ID]Mapping
	releases  map[kernel.ID]ReleaseMapping
	epics     map[kernel.ID]EpicProjection
	sprints   map[kernel.ID][]SprintStatus
	sync      SyncState
	fields    FieldMapping
	processed map[string]struct{}
}

var _ Store = (*MemStore)(nil)

// NewMemStore создаёт пустое хранилище с маппингом по умолчанию.
func NewMemStore() *MemStore {
	return &MemStore{
		mappings: map[kernel.ID]Mapping{}, releases: map[kernel.ID]ReleaseMapping{},
		epics: map[kernel.ID]EpicProjection{}, sprints: map[kernel.ID][]SprintStatus{},
		fields: DefaultFieldMapping(), processed: map[string]struct{}{},
	}
}

func (m *MemStore) SaveMapping(_ context.Context, sc authz.Scope, v Mapping) error {
	v = kernel.CloneValue(v)
	if v.ProductID == kernel.NilID || sc.Product(v.ProductID) < authz.AccessPrivate || !sc.Allows(authz.ActionWriteGraph, v.ProductID) {
		return kernel.ErrForbidden
	}
	if !sc.Valid() {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.mappings[v.FeatureID]; ok && old.ProductID != v.ProductID {
		return kernel.ErrForbidden
	}
	m.mappings[v.FeatureID] = v
	return nil
}

func (m *MemStore) MappingByFeature(_ context.Context, sc authz.Scope, id kernel.ID) (Mapping, error) {
	if !sc.Valid() {
		return Mapping{}, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.mappings[id]
	if !ok {
		return Mapping{}, kernel.NotFound("mapping", id)
	}
	if !sc.Allows(authz.ActionReadPrivate, v.ProductID) {
		return Mapping{}, kernel.ErrForbidden
	}
	return kernel.CloneValue(v), nil
}

func (m *MemStore) MappingByEpic(_ context.Context, sc authz.Scope, key string) (Mapping, error) {
	if !sc.Valid() {
		return Mapping{}, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, v := range m.mappings {
		if v.EpicKey == key {
			if !sc.Allows(authz.ActionReadPrivate, v.ProductID) {
				return Mapping{}, kernel.ErrForbidden
			}
			return kernel.CloneValue(v), nil
		}
	}
	return Mapping{}, kernel.NotFound("mapping "+key, kernel.NilID)
}

func (m *MemStore) Mappings(_ context.Context, sc authz.Scope) ([]Mapping, error) {
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Mapping, 0, len(m.mappings))
	for _, v := range m.mappings {
		if sc.Allows(authz.ActionReadPrivate, v.ProductID) {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EpicKey < out[j].EpicKey })
	return kernel.CloneValue(out), nil
}

func (m *MemStore) SaveReleaseMapping(_ context.Context, sc authz.Scope, v ReleaseMapping) error {
	v = kernel.CloneValue(v)
	if v.ProductID == kernel.NilID || sc.Product(v.ProductID) < authz.AccessPrivate || !sc.Allows(authz.ActionWriteRoadmap, v.ProductID) {
		return kernel.ErrForbidden
	}
	if !sc.Valid() {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.releases[v.ReleaseID]; ok && old.ProductID != v.ProductID {
		return kernel.ErrForbidden
	}
	m.releases[v.ReleaseID] = v
	return nil
}

func (m *MemStore) ReleaseMappings(_ context.Context, sc authz.Scope) ([]ReleaseMapping, error) {
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ReleaseMapping, 0, len(m.releases))
	for _, v := range m.releases {
		if sc.Allows(authz.ActionReadPrivate, v.ProductID) {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FixVersion < out[j].FixVersion })
	return kernel.CloneValue(out), nil
}

func (m *MemStore) SaveEpic(_ context.Context, sc authz.Scope, p EpicProjection) error {
	p = kernel.CloneValue(p)
	if p.ProductID == kernel.NilID || sc.Product(p.ProductID) < authz.AccessPrivate || !sc.Allows(authz.ActionWriteGraph, p.ProductID) {
		return kernel.ErrForbidden
	}
	if !sc.Valid() || !sc.HasRole(authz.RoleService) {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.epics[p.FeatureID]; ok && old.ProductID != p.ProductID {
		return kernel.ErrForbidden
	}
	m.epics[p.FeatureID] = p
	return nil
}

func (m *MemStore) EpicByFeature(_ context.Context, sc authz.Scope, id kernel.ID) (EpicProjection, error) {
	if !sc.Valid() {
		return EpicProjection{}, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.epics[id]
	if !ok {
		return EpicProjection{}, kernel.NotFound("epic projection", id)
	}
	if !sc.Allows(authz.ActionReadPrivate, p.ProductID) {
		return EpicProjection{}, kernel.ErrForbidden
	}
	return kernel.CloneValue(p), nil
}

func (m *MemStore) SaveSprints(_ context.Context, sc authz.Scope, productID kernel.ID, s []SprintStatus) error {
	s = kernel.CloneValue(s)
	for _, row := range s {
		if row.ProductID != productID {
			return kernel.ErrForbidden
		}
	}
	if productID == kernel.NilID || !sc.Allows(authz.ActionReadPrivate, productID) {
		return kernel.ErrForbidden
	}
	if !sc.Valid() || !sc.HasRole(authz.RoleService) {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sprints[productID] = append([]SprintStatus(nil), s...)
	return nil
}

func (m *MemStore) Sprints(_ context.Context, sc authz.Scope, productID kernel.ID) ([]SprintStatus, error) {
	if productID == kernel.NilID || !sc.Allows(authz.ActionReadPrivate, productID) {
		return nil, kernel.ErrForbidden
	}
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return kernel.CloneValue(append([]SprintStatus(nil), m.sprints[productID]...)), nil
}

func (m *MemStore) SaveSyncState(_ context.Context, sc authz.Scope, s SyncState) error {
	s = kernel.CloneValue(s)
	if !sc.Valid() || !sc.HasRole(authz.RoleService) {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sync = s
	return nil
}

func (m *MemStore) SyncState(_ context.Context, sc authz.Scope) (SyncState, error) {
	if !sc.Valid() {
		return SyncState{}, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return kernel.CloneValue(m.sync), nil
}

func (m *MemStore) SaveFieldMapping(_ context.Context, sc authz.Scope, f FieldMapping) error {
	f = kernel.CloneValue(f)
	if !sc.Allows(authz.ActionManageConnects, kernel.NilID) {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fields = f
	return nil
}

func (m *MemStore) FieldMapping(_ context.Context, sc authz.Scope) (FieldMapping, error) {
	if !sc.Valid() {
		return FieldMapping{}, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return kernel.CloneValue(m.fields), nil
}

func (m *MemStore) MarkProcessed(_ context.Context, sc authz.Scope, id string) (bool, error) {
	if !sc.Valid() || !sc.HasRole(authz.RoleService) {
		return false, kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.processed[id]; ok {
		return kernel.CloneValue(false), nil
	}
	m.processed[id] = struct{}{}
	return kernel.CloneValue(true), nil
}
