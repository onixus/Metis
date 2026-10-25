package pgstore

import (
	"context"
	"errors"
	"sort"

	economics "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

func (s *Store) Unification(ctx context.Context, sc authz.Scope, p economics.Period) (economics.HistoryArchive, error) {
	if err := requireRead(sc, true); err != nil {
		return economics.HistoryArchive{}, err
	}
	return get[economics.HistoryArchive](ctx, s, "unification", p.String())
}
func (s *Store) Unifications(ctx context.Context, sc authz.Scope) ([]economics.HistoryArchive, error) {
	if err := requireRead(sc, true); err != nil {
		return nil, err
	}
	out, err := list[economics.HistoryArchive](ctx, s, "unification")
	sort.Slice(out, func(i, j int) bool { return out[i].Period.Before(out[j].Period) })
	return out, err
}
func (s *Store) UnificationBindings(ctx context.Context, sc authz.Scope) ([]economics.HistoryBinding, error) {
	if !sc.Valid() || sc.Finance() < authz.FinanceAggregates {
		return nil, kernel.ErrForbidden
	}
	items, err := list[economics.HistoryArchive](ctx, s, "unification")
	if err != nil {
		return nil, err
	}
	out := []economics.HistoryBinding{}
	for _, a := range items {
		{
			out = append(out, economics.HistoryBinding{OriginalBookVersion: a.BookVersions[len(a.BookVersions)-1].Version, BookProductID: a.BookProductID, Period: a.Period, FieldKeys: a.FieldKeys, MetricKeys: a.MetricKeys})
		}
	}
	return out, nil
}
func (s *Store) SaveUnification(ctx context.Context, sc authz.Scope, a economics.HistoryArchive) error {
	return s.Transact(ctx, sc, func(ctx context.Context) error {
		previous, err := s.Unification(ctx, sc, a.Period)
		if err == nil {
			if previous.Digest == a.Digest {
				return nil
			}
			return kernel.ErrConflict
		}
		if !errors.Is(err, kernel.ErrNotFound) {
			return err
		}
		return s.append(ctx, "unification", a.Period.String(), a.BookProductID, a)
	})
}
