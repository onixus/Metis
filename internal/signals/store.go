package signals

import (
	"context"
	"sync"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// Filter — условия выборки сигналов. Пустое поле — без ограничения.
type Filter struct {
	ProductID  kernel.ID
	FeatureID  kernel.ID
	ContractID kernel.ID
	// HypothesisID — сигналы, привязанные к гипотезе discovery (DS-01).
	HypothesisID kernel.ID
	Statuses     []Status
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
	if f.HypothesisID != kernel.NilID && s.HypothesisID != f.HypothesisID {
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
// Scope проверяется также при прямом вызове реализации.
type Store interface {
	Save(ctx context.Context, sc authz.Scope, s Signal) error
	Get(ctx context.Context, sc authz.Scope, id kernel.ID) (Signal, error)
	// GetByExternalKey ищет сигнал по ключу внешней системы; kernel.ErrNotFound, если нет.
	GetByExternalKey(ctx context.Context, sc authz.Scope, key string) (Signal, error)
	List(ctx context.Context, sc authz.Scope, f Filter) ([]Signal, error)
}

// MemStore — хранилище в памяти.
type MemStore struct {
	mu    sync.Mutex
	items []Signal
}

// NewMemStore создаёт пустое хранилище.
func NewMemStore() *MemStore { return &MemStore{} }

// Save создаёт или обновляет сигнал.
func (m *MemStore) Save(_ context.Context, sc authz.Scope, s Signal) error {
	s = kernel.CloneValue(s)
	if err := sc.Require(authz.ActionWriteSignals, s.ProductID); err != nil {
		return err
	}
	if s.ProductID == kernel.NilID || sc.Product(s.ProductID) < authz.AccessPrivate {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.items {
		if m.items[i].ID == s.ID {
			if m.items[i].ProductID != s.ProductID {
				return kernel.ErrForbidden
			}
			m.items[i] = s
			return nil
		}
	}
	m.items = append(m.items, s)
	return nil
}

// Get возвращает сигнал по идентификатору.
func (m *MemStore) Get(_ context.Context, sc authz.Scope, id kernel.ID) (Signal, error) {
	if !sc.Valid() {
		return Signal{}, kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.items {
		if s.ID == id {
			if err := sc.Require(authz.ActionReadPrivate, s.ProductID); err != nil {
				return Signal{}, err
			}
			return kernel.CloneValue(s), nil
		}
	}
	return Signal{}, kernel.NotFound("signal", id)
}

// GetByExternalKey ищет сигнал по внешнему ключу.
func (m *MemStore) GetByExternalKey(_ context.Context, sc authz.Scope, key string) (Signal, error) {
	if !sc.Valid() {
		return Signal{}, kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if key != "" {
		for _, s := range m.items {
			if s.ExternalKey == key {
				if err := sc.Require(authz.ActionReadPrivate, s.ProductID); err != nil {
					return Signal{}, err
				}
				return kernel.CloneValue(s), nil
			}
		}
	}
	return Signal{}, kernel.ErrNotFound
}

// List возвращает сигналы по фильтру в порядке сохранения.
func (m *MemStore) List(_ context.Context, sc authz.Scope, f Filter) ([]Signal, error) {
	f = kernel.CloneValue(f)
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	if f.ProductID != kernel.NilID {
		if err := sc.Require(authz.ActionReadPrivate, f.ProductID); err != nil {
			return nil, err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Signal, 0, len(m.items))
	for _, s := range m.items {
		if f.matches(s) && sc.Allows(authz.ActionReadPrivate, s.ProductID) {
			out = append(out, s)
		}
	}
	return kernel.CloneValue(out), nil
}
