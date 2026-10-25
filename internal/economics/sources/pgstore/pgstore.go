// Package pgstore persists immutable financial formula source aggregates.
package pgstore

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/onixus/metis/internal/economics/internal/db"
	"github.com/onixus/metis/internal/economics/sources"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/pgdb"
)

type Store struct{ database *pgdb.DB }

func New(database *pgdb.DB) *Store                 { return &Store{database: database} }
func (s *Store) q(ctx context.Context) *db.Queries { return db.New(pgdb.Querier(ctx, s.database)) }
func (s *Store) Transact(ctx context.Context, sc authz.Scope, fn func(context.Context) error) error {
	if !sc.Allows(authz.ActionWriteModelFinance, kernel.NilID) || sc.Finance() < authz.FinanceFull {
		return kernel.ErrForbidden
	}
	return s.database.Transact(ctx, func(ctx context.Context) error {
		if err := pgdb.LockApplication(ctx, s.database); err != nil {
			return err
		}
		return fn(ctx)
	})
}
func (s *Store) Latest(ctx context.Context, sc authz.Scope, k sources.Key) (sources.Snapshot, error) {
	if err := sources.Require(sc, k, false); err != nil {
		return sources.Snapshot{}, err
	}
	raw, err := s.q(ctx).GetFormulaSource(ctx, db.GetFormulaSourceParams{ProductID: k.ProductID, Period: k.Period, Kind: k.Kind, TeamID: k.TeamID, Portfolio: k.Portfolio})
	if err != nil {
		return sources.Snapshot{}, fmt.Errorf("formula source: %w", pgdb.MapError(err))
	}
	return decode(raw, k)
}
func decode(raw []byte, k sources.Key) (sources.Snapshot, error) {
	var out sources.Snapshot
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("decode formula source: %w", err)
	}
	if out.Key != k {
		return sources.Snapshot{}, kernel.ErrForbidden
	}
	return out, nil
}
func (s *Store) History(ctx context.Context, sc authz.Scope, k sources.Key) ([]sources.Snapshot, error) {
	if err := sources.Require(sc, k, false); err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListFormulaSources(ctx, db.ListFormulaSourcesParams{ProductID: k.ProductID, Period: k.Period, Kind: k.Kind, TeamID: k.TeamID, Portfolio: k.Portfolio})
	if err != nil {
		return nil, fmt.Errorf("source history: %w", pgdb.MapError(err))
	}
	out := make([]sources.Snapshot, 0, len(rows))
	for _, raw := range rows {
		v, err := decode(raw, k)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *Store) Append(ctx context.Context, sc authz.Scope, v sources.Snapshot, expected int) error {
	if err := sources.Require(sc, v.Key, true); err != nil {
		return err
	}
	if expected < 0 || expected >= math.MaxInt32 || v.Version < 1 || v.Version > math.MaxInt32 || v.Version != expected+1 {
		return kernel.ErrConflict
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode source: %w", err)
	}
	count, err := s.q(ctx).AppendFormulaSource(ctx, db.AppendFormulaSourceParams{ID: v.ID, ProductID: v.ProductID, Period: v.Period, Kind: v.Kind, TeamID: v.TeamID, Portfolio: v.Portfolio, Version: int32(v.Version), ExpectedVersion: int32(expected), Body: raw, CreatedAt: v.CreatedAt})
	if err != nil {
		return fmt.Errorf("append source: %w", pgdb.MapError(err))
	}
	if count != 1 {
		return kernel.ErrConflict
	}
	return nil
}
