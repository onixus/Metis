package signals

import (
	"context"
	"sync"

	"github.com/onixus/metis/internal/kernel"
)

// Filter — условия выборки сигналов. Пустое поле — без ограничения.
type Filter struct {
	ProductID  kernel.ID
	FeatureID  kernel.ID
	ContractID kernel.ID
	Statuses   []Status
}

func (f Filter) matches(s Signal) bool {
	if f.ProductID != kernel.NilID && s.ProductID != f.ProductID {
		return false
	}
	if f.FeatureID != kernel.NilID && s.FeatureID != f.FeatureID {
		return false
	}
	if f.ContractID != kernel.NilID && s.ContractID != f.ContractID {
		return false
	}
	if len(f.Statuses) > 0 {
		ok := false
		for _, st := range f.Statuses {
			if s.Status == st {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// Store — хранилище сигналов. Реализации: память (тесты, стенд), PostgreSQL (internal/pg).
// Авторизация выполняется в Service до вызова хранилища.
type Store interface {
	Save(ctx context.Context, s Signal) error
	Get(ctx context.Context, id kernel.ID) (Signal, error)
	// GetByExternalKey ищет сигнал по ключу внешней системы; kernel.ErrNotFound, если нет.
	GetByExternalKey(ctx context.Context, key string) (Signal, error)
	List(ctx context.Context, f Filter) ([]Signal, error)
}

// MemStore — хранилище в памяти.
type MemStore struct {
	mu    sync.Mutex
	items []Signal
}

// NewMemStore создаёт пустое хранилище.
func NewMemStore() *MemStore { return &MemStore{} }

// Save создаёт или обновляет сигнал.
func (m *MemStore) Save(_ context.Context, s Signal) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.items {
		if m.items[i].ID == s.ID {
			m.items[i] = s
			return nil
		}
	}
	m.items = append(m.items, s)
	return nil
}

// Get возвращает сигнал по идентификатору.
func (m *MemStore) Get(_ context.Context, id kernel.ID) (Signal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.items {
		if s.ID == id {
			return s, nil
		}
	}
	return Signal{}, kernel.NotFound("signal", id)
}

// GetByExternalKey ищет сигнал по внешнему ключу.
func (m *MemStore) GetByExternalKey(_ context.Context, key string) (Signal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if key != "" {
		for _, s := range m.items {
			if s.ExternalKey == key {
				return s, nil
			}
		}
	}
	return Signal{}, kernel.ErrNotFound
}

// List возвращает сигналы по фильтру в порядке сохранения.
func (m *MemStore) List(_ context.Context, f Filter) ([]Signal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Signal, 0, len(m.items))
	for _, s := range m.items {
		if f.matches(s) {
			out = append(out, s)
		}
	}
	return out, nil
}
