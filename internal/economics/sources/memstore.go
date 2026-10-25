package sources

import (
	"context"
	"sync"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

type MemStore struct {
	mu     sync.RWMutex
	values map[Key][]Snapshot
}

func NewMemStore() *MemStore { return &MemStore{values: map[Key][]Snapshot{}} }
func (s *MemStore) Latest(ctx context.Context, sc authz.Scope, k Key) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if err := Require(sc, k, false); err != nil {
		return Snapshot{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	v := s.values[k]
	if len(v) == 0 {
		return Snapshot{}, kernel.ErrNotFound
	}
	return kernel.CloneValue(v[len(v)-1]), nil
}
func (s *MemStore) History(ctx context.Context, sc authz.Scope, k Key) ([]Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := Require(sc, k, false); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return kernel.CloneValue(s.values[k]), nil
}
func (s *MemStore) Append(ctx context.Context, sc authz.Scope, v Snapshot, expected int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := Require(sc, v.Key, true); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.values[v.Key]) != expected || v.Version != expected+1 {
		return kernel.ErrConflict
	}
	s.values[v.Key] = append(s.values[v.Key], kernel.CloneValue(v))
	return nil
}
