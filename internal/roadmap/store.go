package roadmap

import (
	"context"
	"sync"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// Store — хранилище roadmap. Авторизация проверяется также при прямом вызове реализации.
type Store interface {
	SaveItem(ctx context.Context, sc authz.Scope, it RoadmapItem) error
	Item(ctx context.Context, sc authz.Scope, id kernel.ID) (RoadmapItem, error)
	Items(ctx context.Context, sc authz.Scope, productID kernel.ID) ([]RoadmapItem, error)
	// ItemsByFeature возвращает элементы, привязанные к фиче (во всех продуктах).
	ItemsByFeature(ctx context.Context, sc authz.Scope, featureID kernel.ID) ([]RoadmapItem, error)
	// ItemByCommitment возвращает элемент, созданный по обязательству (CT-04); ErrNotFound, если его нет.
	ItemByCommitment(ctx context.Context, sc authz.Scope, commitmentID kernel.ID) (RoadmapItem, error)
	SaveRelease(ctx context.Context, sc authz.Scope, r Release) error
	Release(ctx context.Context, sc authz.Scope, id kernel.ID) (Release, error)
	Releases(ctx context.Context, sc authz.Scope, productID kernel.ID) ([]Release, error)
	AppendDateChange(ctx context.Context, sc authz.Scope, ch DateChange) error
	DateHistory(ctx context.Context, sc authz.Scope, itemID kernel.ID) ([]DateChange, error)
	// EventProcessed сообщает, обрабатывалось ли событие (идемпотентность обработчиков по Event.ID).
	EventProcessed(ctx context.Context, sc authz.Scope, eventID kernel.ID) (bool, error)
	// MarkEventProcessed отмечает событие обработанным; в SQL-реализации — в одной транзакции с записью истории.
	MarkEventProcessed(ctx context.Context, sc authz.Scope, eventID kernel.ID) error
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
func (m *MemStore) SaveItem(_ context.Context, sc authz.Scope, it RoadmapItem) error {
	it = kernel.CloneValue(it)
	if !sc.Valid() {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := sc.Require(authz.ActionWriteRoadmap, it.ProductID); err != nil {
		return err
	}
	if it.ProductID == kernel.NilID || sc.Product(it.ProductID) < authz.AccessPrivate {
		return kernel.ErrForbidden
	}
	if old, ok := m.items[it.ID]; ok && old.ProductID != it.ProductID {
		return kernel.ErrForbidden
	}
	m.items[it.ID] = it
	return nil
}

// Item возвращает элемент по идентификатору.
func (m *MemStore) Item(_ context.Context, sc authz.Scope, id kernel.ID) (RoadmapItem, error) {
	if !sc.Valid() {
		return RoadmapItem{}, kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.items[id]
	if !ok {
		return RoadmapItem{}, kernel.NotFound("roadmap_item", id)
	}
	if !sc.Allows(authz.ActionReadStrategic, it.ProductID) {
		return RoadmapItem{}, kernel.ErrForbidden
	}
	return kernel.CloneValue(it), nil
}

// Items возвращает элементы продукта.
func (m *MemStore) Items(_ context.Context, sc authz.Scope, productID kernel.ID) ([]RoadmapItem, error) {
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	if !sc.Allows(authz.ActionReadStrategic, productID) {
		return nil, kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]RoadmapItem, 0)
	for _, it := range m.items {
		if it.ProductID == productID {
			out = append(out, it)
		}
	}
	return kernel.CloneValue(out), nil
}

// ItemsByFeature возвращает элементы, привязанные к фиче.
func (m *MemStore) ItemsByFeature(_ context.Context, sc authz.Scope, featureID kernel.ID) ([]RoadmapItem, error) {
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]RoadmapItem, 0)
	for _, it := range m.items {
		if it.FeatureID == featureID && sc.Allows(authz.ActionReadStrategic, it.ProductID) {
			out = append(out, it)
		}
	}
	return kernel.CloneValue(out), nil
}

// ItemByCommitment возвращает элемент по обязательству.
func (m *MemStore) ItemByCommitment(_ context.Context, sc authz.Scope, commitmentID kernel.ID) (RoadmapItem, error) {
	if !sc.Valid() {
		return RoadmapItem{}, kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, it := range m.items {
		if it.CommitmentID == commitmentID {
			if !sc.Allows(authz.ActionReadStrategic, it.ProductID) {
				return RoadmapItem{}, kernel.ErrForbidden
			}
			return kernel.CloneValue(it), nil
		}
	}
	return RoadmapItem{}, kernel.NotFound("roadmap_item", commitmentID)
}

// SaveRelease сохраняет релиз.
func (m *MemStore) SaveRelease(_ context.Context, sc authz.Scope, r Release) error {
	r = kernel.CloneValue(r)
	if !sc.Valid() {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := sc.Require(authz.ActionWriteRoadmap, r.ProductID); err != nil {
		return err
	}
	if r.ProductID == kernel.NilID || sc.Product(r.ProductID) < authz.AccessPrivate {
		return kernel.ErrForbidden
	}
	if old, ok := m.releases[r.ID]; ok && old.ProductID != r.ProductID {
		return kernel.ErrForbidden
	}
	r.FeatureIDs = append([]kernel.ID(nil), r.FeatureIDs...)
	r.CompatibilityMatrix = nil // вычисляемое поле не хранится
	m.releases[r.ID] = r
	return nil
}

// Release возвращает релиз по идентификатору.
func (m *MemStore) Release(_ context.Context, sc authz.Scope, id kernel.ID) (Release, error) {
	if !sc.Valid() {
		return Release{}, kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.releases[id]
	if !ok {
		return Release{}, kernel.NotFound("release", id)
	}
	if !sc.Allows(authz.ActionReadStrategic, r.ProductID) {
		return Release{}, kernel.ErrForbidden
	}
	return kernel.CloneValue(r), nil
}

// Releases возвращает релизы продукта.
func (m *MemStore) Releases(_ context.Context, sc authz.Scope, productID kernel.ID) ([]Release, error) {
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	if !sc.Allows(authz.ActionReadStrategic, productID) {
		return nil, kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Release, 0)
	for _, r := range m.releases {
		if r.ProductID == productID {
			out = append(out, r)
		}
	}
	return kernel.CloneValue(out), nil
}

// AppendDateChange добавляет запись истории (только INSERT).
func (m *MemStore) AppendDateChange(_ context.Context, sc authz.Scope, ch DateChange) error {
	ch = kernel.CloneValue(ch)
	if !sc.Valid() {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.items[ch.ItemID]
	if !ok || ch.ProductID != it.ProductID || sc.Product(it.ProductID) < authz.AccessPrivate || !sc.Allows(authz.ActionWriteRoadmap, it.ProductID) {
		return kernel.ErrForbidden
	}
	m.history[ch.ItemID] = append(m.history[ch.ItemID], ch)
	return nil
}

// DateHistory возвращает историю дат элемента в порядке записи.
func (m *MemStore) DateHistory(_ context.Context, sc authz.Scope, itemID kernel.ID) ([]DateChange, error) {
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.items[itemID]
	if !ok || !sc.Allows(authz.ActionReadStrategic, it.ProductID) {
		return nil, kernel.ErrForbidden
	}
	return kernel.CloneValue(append([]DateChange(nil), m.history[itemID]...)), nil
}

// EventProcessed сообщает, обрабатывалось ли событие.
func (m *MemStore) EventProcessed(_ context.Context, sc authz.Scope, eventID kernel.ID) (bool, error) {
	if !sc.Valid() || !sc.HasRole(authz.RoleService) {
		return false, kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.processed[eventID]
	return kernel.CloneValue(ok), nil
}

// MarkEventProcessed отмечает событие обработанным.
func (m *MemStore) MarkEventProcessed(_ context.Context, sc authz.Scope, eventID kernel.ID) error {
	if !sc.Valid() || !sc.HasRole(authz.RoleService) {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.processed[eventID] = struct{}{}
	return nil
}
