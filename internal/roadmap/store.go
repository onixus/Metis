package roadmap

import (
	"context"
	"sync"

	"github.com/onixus/metis/internal/kernel"
)

// Store — хранилище roadmap. Авторизация выполняется в Service до вызова хранилища.
type Store interface {
	SaveItem(ctx context.Context, it RoadmapItem) error
	Item(ctx context.Context, id kernel.ID) (RoadmapItem, error)
	Items(ctx context.Context, productID kernel.ID) ([]RoadmapItem, error)
	// ItemsByFeature возвращает элементы, привязанные к фиче (во всех продуктах).
	ItemsByFeature(ctx context.Context, featureID kernel.ID) ([]RoadmapItem, error)
	SaveRelease(ctx context.Context, r Release) error
	Release(ctx context.Context, id kernel.ID) (Release, error)
	Releases(ctx context.Context, productID kernel.ID) ([]Release, error)
	AppendDateChange(ctx context.Context, ch DateChange) error
	DateHistory(ctx context.Context, itemID kernel.ID) ([]DateChange, error)
	// EventProcessed сообщает, обрабатывалось ли событие (идемпотентность обработчиков по Event.ID).
	EventProcessed(ctx context.Context, eventID kernel.ID) (bool, error)
	// MarkEventProcessed отмечает событие обработанным; в SQL-реализации — в одной транзакции с записью истории.
	MarkEventProcessed(ctx context.Context, eventID kernel.ID) error
}

// MemStore — хранилище в памяти (тесты, стенд).
type MemStore struct {
	mu        sync.Mutex
	items     map[kernel.ID]RoadmapItem
	releases  map[kernel.ID]Release
	history   map[kernel.ID][]DateChange
	processed map[kernel.ID]struct{}
}

// NewMemStore создаёт пустое хранилище.
func NewMemStore() *MemStore {
	return &MemStore{
		items:     map[kernel.ID]RoadmapItem{},
		releases:  map[kernel.ID]Release{},
		history:   map[kernel.ID][]DateChange{},
		processed: map[kernel.ID]struct{}{},
	}
}

// SaveItem сохраняет элемент.
func (m *MemStore) SaveItem(_ context.Context, it RoadmapItem) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[it.ID] = it
	return nil
}

// Item возвращает элемент по идентификатору.
func (m *MemStore) Item(_ context.Context, id kernel.ID) (RoadmapItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.items[id]
	if !ok {
		return RoadmapItem{}, kernel.NotFound("roadmap_item", id)
	}
	return it, nil
}

// Items возвращает элементы продукта.
func (m *MemStore) Items(_ context.Context, productID kernel.ID) ([]RoadmapItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]RoadmapItem, 0)
	for _, it := range m.items {
		if it.ProductID == productID {
			out = append(out, it)
		}
	}
	return out, nil
}

// ItemsByFeature возвращает элементы, привязанные к фиче.
func (m *MemStore) ItemsByFeature(_ context.Context, featureID kernel.ID) ([]RoadmapItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]RoadmapItem, 0)
	for _, it := range m.items {
		if it.FeatureID == featureID {
			out = append(out, it)
		}
	}
	return out, nil
}

// SaveRelease сохраняет релиз.
func (m *MemStore) SaveRelease(_ context.Context, r Release) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releases[r.ID] = r
	return nil
}

// Release возвращает релиз по идентификатору.
func (m *MemStore) Release(_ context.Context, id kernel.ID) (Release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.releases[id]
	if !ok {
		return Release{}, kernel.NotFound("release", id)
	}
	return r, nil
}

// Releases возвращает релизы продукта.
func (m *MemStore) Releases(_ context.Context, productID kernel.ID) ([]Release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Release, 0)
	for _, r := range m.releases {
		if r.ProductID == productID {
			out = append(out, r)
		}
	}
	return out, nil
}

// AppendDateChange добавляет запись истории (только INSERT).
func (m *MemStore) AppendDateChange(_ context.Context, ch DateChange) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.history[ch.ItemID] = append(m.history[ch.ItemID], ch)
	return nil
}

// DateHistory возвращает историю дат элемента в порядке записи.
func (m *MemStore) DateHistory(_ context.Context, itemID kernel.ID) ([]DateChange, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]DateChange(nil), m.history[itemID]...), nil
}

// EventProcessed сообщает, обрабатывалось ли событие.
func (m *MemStore) EventProcessed(_ context.Context, eventID kernel.ID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.processed[eventID]
	return ok, nil
}

// MarkEventProcessed отмечает событие обработанным.
func (m *MemStore) MarkEventProcessed(_ context.Context, eventID kernel.ID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.processed[eventID] = struct{}{}
	return nil
}
