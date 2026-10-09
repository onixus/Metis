package audit

import (
	"context"
	"sync"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// MemStore — хранилище в памяти для тестов и стендов без БД.
type MemStore struct {
	mu   sync.RWMutex
	recs []Record
}

// NewMemStore создаёт пустое хранилище.
func NewMemStore() *MemStore { return &MemStore{} }

// Last — последняя запись.
func (m *MemStore) Last(_ context.Context, sc authz.Scope) (Record, error) {
	if err := authz.RequireInfrastructure(sc); err != nil {
		return Record{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.recs) == 0 {
		return Record{}, kernel.ErrNotFound
	}
	return kernel.CloneValue(m.recs[len(m.recs)-1]), nil
}

// Insert добавляет запись.
func (m *MemStore) Insert(_ context.Context, sc authz.Scope, r Record) error {
	if err := authz.RequireInfrastructure(sc); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.recs) > 0 && m.recs[len(m.recs)-1].Seq >= r.Seq {
		return kernel.ErrConflict
	}
	m.recs = append(m.recs, kernel.CloneValue(r))
	return nil
}

// Walk перебирает записи.
func (m *MemStore) Walk(_ context.Context, sc authz.Scope, fn func(Record) error) error {
	if err := authz.RequireInfrastructure(sc); err != nil {
		return err
	}
	m.mu.RLock()
	snapshot := kernel.CloneValue(m.recs)
	m.mu.RUnlock()
	for _, r := range snapshot {
		if err := fn(r); err != nil {
			return err
		}
	}
	return nil
}

// Tamper подменяет содержимое записи (только для тестов проверки целостности).
func (m *MemStore) Tamper(seq int64, mutate func(*Record)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.recs {
		if m.recs[i].Seq == seq {
			mutate(&m.recs[i])
		}
	}
}
