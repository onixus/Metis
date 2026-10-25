package economics

import (
	"context"
	"sort"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

func (m *MemStore) Unification(_ context.Context, sc authz.Scope, p Period) (HistoryArchive, error) {
	if !sc.Valid() || sc.Finance() < authz.FinanceFull || !sc.SeesAllProducts() {
		return HistoryArchive{}, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, ok := m.unifications[p.String()]
	if !ok {
		return HistoryArchive{}, kernel.ErrNotFound
	}
	return kernel.CloneValue(a), nil
}
func (m *MemStore) Unifications(_ context.Context, sc authz.Scope) ([]HistoryArchive, error) {
	if !sc.Valid() || sc.Finance() < authz.FinanceFull || !sc.SeesAllProducts() {
		return nil, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []HistoryArchive{}
	for _, a := range m.unifications {
		out = append(out, kernel.CloneValue(a))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Period.Before(out[j].Period) })
	return out, nil
}
func (m *MemStore) UnificationBindings(_ context.Context, sc authz.Scope) ([]HistoryBinding, error) {
	if !sc.Valid() || sc.Finance() < authz.FinanceAggregates {
		return nil, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []HistoryBinding{}
	for _, a := range m.unifications {
		{
			out = append(out, HistoryBinding{OriginalBookVersion: a.BookVersions[len(a.BookVersions)-1].Version, BookProductID: a.BookProductID, Period: a.Period, FieldKeys: kernel.CloneValue(a.FieldKeys), MetricKeys: kernel.CloneValue(a.MetricKeys)})
		}
	}
	return out, nil
}
func (m *MemStore) SaveUnification(_ context.Context, sc authz.Scope, a HistoryArchive) error {
	if !sc.Allows(authz.ActionWriteModelFinance, kernel.NilID) || !sc.SeesAllProducts() {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.unifications == nil {
		m.unifications = map[string]HistoryArchive{}
	}
	if previous, ok := m.unifications[a.Period.String()]; ok {
		if previous.Digest != a.Digest {
			return kernel.ErrConflict
		}
		return nil
	}
	m.unifications[a.Period.String()] = kernel.CloneValue(a)
	return nil
}
