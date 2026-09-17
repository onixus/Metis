package delivery

import (
	"context"
	"sort"
	"sync"

	"github.com/onixus/metis/internal/kernel"
)

// Store — хранилище проекции трекера. Реализация на PostgreSQL — в internal/ (итерация db).
type Store interface {
	SaveMapping(ctx context.Context, m Mapping) error
	MappingByFeature(ctx context.Context, featureID kernel.ID) (Mapping, error)
	MappingByEpic(ctx context.Context, epicKey string) (Mapping, error)
	Mappings(ctx context.Context) ([]Mapping, error)
	SaveReleaseMapping(ctx context.Context, m ReleaseMapping) error
	ReleaseMappings(ctx context.Context) ([]ReleaseMapping, error)

	SaveEpic(ctx context.Context, p EpicProjection) error
	EpicByFeature(ctx context.Context, featureID kernel.ID) (EpicProjection, error)
	SaveSprints(ctx context.Context, productID kernel.ID, sprints []SprintStatus) error
	Sprints(ctx context.Context, productID kernel.ID) ([]SprintStatus, error)

	SaveSyncState(ctx context.Context, s SyncState) error
	SyncState(ctx context.Context) (SyncState, error)
	SaveFieldMapping(ctx context.Context, m FieldMapping) error
	FieldMapping(ctx context.Context) (FieldMapping, error)

	// MarkProcessed запоминает внешний ключ события; возвращает false, если событие уже обработано (ТЗ 4.2).
	MarkProcessed(ctx context.Context, externalID string) (bool, error)
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

func (m *MemStore) SaveMapping(_ context.Context, v Mapping) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mappings[v.FeatureID] = v
	return nil
}

func (m *MemStore) MappingByFeature(_ context.Context, id kernel.ID) (Mapping, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.mappings[id]
	if !ok {
		return Mapping{}, kernel.NotFound("mapping", id)
	}
	return v, nil
}

func (m *MemStore) MappingByEpic(_ context.Context, key string) (Mapping, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, v := range m.mappings {
		if v.EpicKey == key {
			return v, nil
		}
	}
	return Mapping{}, kernel.NotFound("mapping "+key, kernel.NilID)
}

func (m *MemStore) Mappings(context.Context) ([]Mapping, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Mapping, 0, len(m.mappings))
	for _, v := range m.mappings {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EpicKey < out[j].EpicKey })
	return out, nil
}

func (m *MemStore) SaveReleaseMapping(_ context.Context, v ReleaseMapping) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releases[v.ReleaseID] = v
	return nil
}

func (m *MemStore) ReleaseMappings(context.Context) ([]ReleaseMapping, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ReleaseMapping, 0, len(m.releases))
	for _, v := range m.releases {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FixVersion < out[j].FixVersion })
	return out, nil
}

func (m *MemStore) SaveEpic(_ context.Context, p EpicProjection) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.epics[p.FeatureID] = p
	return nil
}

func (m *MemStore) EpicByFeature(_ context.Context, id kernel.ID) (EpicProjection, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.epics[id]
	if !ok {
		return EpicProjection{}, kernel.NotFound("epic projection", id)
	}
	return p, nil
}

func (m *MemStore) SaveSprints(_ context.Context, productID kernel.ID, s []SprintStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sprints[productID] = append([]SprintStatus(nil), s...)
	return nil
}

func (m *MemStore) Sprints(_ context.Context, productID kernel.ID) ([]SprintStatus, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]SprintStatus(nil), m.sprints[productID]...), nil
}

func (m *MemStore) SaveSyncState(_ context.Context, s SyncState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sync = s
	return nil
}

func (m *MemStore) SyncState(context.Context) (SyncState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sync, nil
}

func (m *MemStore) SaveFieldMapping(_ context.Context, f FieldMapping) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fields = f
	return nil
}

func (m *MemStore) FieldMapping(context.Context) (FieldMapping, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.fields, nil
}

func (m *MemStore) MarkProcessed(_ context.Context, id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.processed[id]; ok {
		return false, nil
	}
	m.processed[id] = struct{}{}
	return true, nil
}
