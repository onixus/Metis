package economics

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// Store persists whole immutable versions. Append must atomically compare the latest version.
type Store interface {
	Snapshot(ctx context.Context, sc authz.Scope, productID kernel.ID, period string, version int) (Snapshot, error)
	History(ctx context.Context, sc authz.Scope, productID kernel.ID, period string) ([]SnapshotInfo, error)
	Append(ctx context.Context, sc authz.Scope, snapshot Snapshot, expectedVersion int) error
	UpsertTemplate(ctx context.Context, sc authz.Scope, template ImportTemplate) error
	ListTemplates(ctx context.Context, sc authz.Scope, productID kernel.ID) ([]ImportTemplate, error)
}

// RequireSnapshot prevents cross-product source data from escaping through an owning book.
func RequireSnapshot(sc authz.Scope, snapshot Snapshot, action authz.Action) error {
	if err := sc.Require(action, snapshot.ProductID); err != nil {
		return err
	}
	for _, row := range snapshot.Rows {
		if err := sc.Require(action, row.ProductID); err != nil {
			return err
		}
		for _, a := range row.Allocations {
			if err := sc.Require(action, a.ProductID); err != nil {
				return err
			}
		}
	}
	return nil
}

type MemStore struct {
	mu        sync.RWMutex
	books     map[string][]Snapshot
	templates map[string]ImportTemplate
}

func NewMemStore() *MemStore {
	return &MemStore{books: make(map[string][]Snapshot), templates: make(map[string]ImportTemplate)}
}

func bookKey(product kernel.ID, period string) string { return product.String() + "/" + period }

func cloneSnapshot(s Snapshot) (Snapshot, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return Snapshot{}, fmt.Errorf("encode snapshot: %w", err)
	}
	var out Snapshot
	if err := json.Unmarshal(data, &out); err != nil {
		return Snapshot{}, fmt.Errorf("decode snapshot: %w", err)
	}
	return out, nil
}

func (m *MemStore) Snapshot(_ context.Context, sc authz.Scope, product kernel.ID, period string, version int) (Snapshot, error) {
	if err := sc.Require(authz.ActionReadFinance, product); err != nil {
		return Snapshot{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	rows := m.books[bookKey(product, period)]
	if len(rows) == 0 {
		return Snapshot{}, kernel.ErrNotFound
	}
	if version == 0 {
		version = len(rows)
	}
	if version < 1 || version > len(rows) {
		return Snapshot{}, kernel.ErrNotFound
	}
	snapshot := rows[version-1]
	if err := RequireSnapshot(sc, snapshot, authz.ActionReadFinance); err != nil {
		return Snapshot{}, err
	}
	return cloneSnapshot(snapshot)
}

func (m *MemStore) History(_ context.Context, sc authz.Scope, product kernel.ID, period string) ([]SnapshotInfo, error) {
	if err := sc.Require(authz.ActionReadFinance, product); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	infos := make([]SnapshotInfo, 0)
	for _, snapshot := range m.books[bookKey(product, period)] {
		if err := RequireSnapshot(sc, snapshot, authz.ActionReadFinance); err != nil {
			return nil, err
		}
		infos = append(infos, snapshot.SnapshotInfo)
	}
	return infos, nil
}

func (m *MemStore) Append(_ context.Context, sc authz.Scope, snapshot Snapshot, expectedVersion int) error {
	if err := RequireSnapshot(sc, snapshot, authz.ActionWriteFinance); err != nil {
		return err
	}
	if expectedVersion < 0 || snapshot.Version != expectedVersion+1 {
		return kernel.ErrConflict
	}
	copy, err := cloneSnapshot(snapshot)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := bookKey(snapshot.ProductID, snapshot.Period)
	if len(m.books[key]) != expectedVersion {
		return kernel.ErrConflict
	}
	m.books[key] = append(m.books[key], copy)
	return nil
}

func (m *MemStore) UpsertTemplate(_ context.Context, sc authz.Scope, template ImportTemplate) error {
	if err := sc.Require(authz.ActionWriteFinance, template.ProductID); err != nil {
		return err
	}
	copy, err := copyTemplate(template)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.templates[bookKey(template.ProductID, template.Name)] = copy
	return nil
}

func copyTemplate(template ImportTemplate) (ImportTemplate, error) {
	raw, err := json.Marshal(template)
	if err != nil {
		return ImportTemplate{}, fmt.Errorf("encode financial template: %w", err)
	}
	var copy ImportTemplate
	if err := json.Unmarshal(raw, &copy); err != nil {
		return ImportTemplate{}, fmt.Errorf("decode financial template: %w", err)
	}
	return copy, nil
}

func (m *MemStore) ListTemplates(_ context.Context, sc authz.Scope, product kernel.ID) ([]ImportTemplate, error) {
	if err := sc.Require(authz.ActionReadFinance, product); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []ImportTemplate{}
	for _, template := range m.templates {
		if template.ProductID == product {
			copy, err := copyTemplate(template)
			if err != nil {
				return nil, err
			}
			out = append(out, copy)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
