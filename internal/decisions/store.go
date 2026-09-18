package decisions

import (
	"context"
	"sort"
	"sync"

	"github.com/onixus/metis/internal/kernel"
)

// Filter — условия выборки решений. Пустое поле — без ограничения.
type Filter struct {
	// ProductID — фильтр по продукту; HasProduct = true с NilID выбирает портфельные решения.
	ProductID  kernel.ID
	HasProduct bool
	Status     Status
	// Link — решения, связанные с сущностью (DS-04).
	Link *Link
}

func (f Filter) matches(r DecisionRecord) bool {
	if f.HasProduct && r.ProductID != f.ProductID {
		return false
	}
	if f.Status != "" && r.Status != f.Status {
		return false
	}
	if f.Link != nil {
		found := false
		for _, l := range r.Links {
			if l == *f.Link {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// Store — хранилище решений. Реализации: память (тесты, стенд), PostgreSQL (internal/pg).
// Авторизация выполняется в Service до вызова хранилища.
type Store interface {
	Save(ctx context.Context, r DecisionRecord) error
	Get(ctx context.Context, id kernel.ID) (DecisionRecord, error)
	List(ctx context.Context, f Filter) ([]DecisionRecord, error)
	// DueForReview возвращает принятые решения с датой ревизии не позже указанной и без ревизии (DA-06).
	DueForReview(ctx context.Context, on kernel.Date) ([]DecisionRecord, error)
	// EventProcessed сообщает, обрабатывалось ли событие (идемпотентность обработчиков по Event.ID).
	EventProcessed(ctx context.Context, eventID kernel.ID) (bool, error)
	// MarkEventProcessed отмечает событие обработанным; в SQL-реализации — в одной транзакции с записью PageID.
	MarkEventProcessed(ctx context.Context, eventID kernel.ID) error
}

// MemStore — хранилище в памяти.
type MemStore struct {
	mu        sync.Mutex
	items     []DecisionRecord
	processed map[kernel.ID]struct{}
}

// NewMemStore создаёт пустое хранилище.
func NewMemStore() *MemStore { return &MemStore{processed: map[kernel.ID]struct{}{}} }

var _ Store = (*MemStore)(nil)

// Save создаёт или обновляет решение.
func (m *MemStore) Save(_ context.Context, r DecisionRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.items {
		if m.items[i].ID == r.ID {
			m.items[i] = r
			return nil
		}
	}
	m.items = append(m.items, r)
	return nil
}

// Get возвращает решение по идентификатору.
func (m *MemStore) Get(_ context.Context, id kernel.ID) (DecisionRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.items {
		if r.ID == id {
			return r, nil
		}
	}
	return DecisionRecord{}, kernel.NotFound("decision", id)
}

// List возвращает решения по фильтру в порядке сохранения.
func (m *MemStore) List(_ context.Context, f Filter) ([]DecisionRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]DecisionRecord, 0, len(m.items))
	for _, r := range m.items {
		if f.matches(r) {
			out = append(out, r)
		}
	}
	return out, nil
}

// DueForReview возвращает принятые решения с наступившей датой ревизии и без ревизии (DA-06).
func (m *MemStore) DueForReview(_ context.Context, on kernel.Date) ([]DecisionRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]DecisionRecord, 0)
	for _, r := range m.items {
		if r.Review != nil || r.Status != StatusAccepted || r.ReviewDate.IsZero() || r.ReviewDate.After(on) {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ReviewDate != out[j].ReviewDate {
			return out[i].ReviewDate.Before(out[j].ReviewDate)
		}
		return out[i].ID.String() < out[j].ID.String()
	})
	return out, nil
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
