// Package pgstore persists immutable economics versions in their own schema.
package pgstore

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/onixus/metis/internal/economics"
	"github.com/onixus/metis/internal/economics/internal/db"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/pgdb"
)

type Store struct{ database *pgdb.DB }

var _ economics.Store = (*Store)(nil)

func New(database *pgdb.DB) *Store { return &Store{database: database} }
func (s *Store) queries(ctx context.Context) *db.Queries {
	return db.New(pgdb.Querier(ctx, s.database))
}

func (s *Store) Snapshot(ctx context.Context, sc authz.Scope, product kernel.ID, period string, version int) (economics.Snapshot, error) {
	if err := sc.Require(authz.ActionReadFinance, product); err != nil {
		return economics.Snapshot{}, err
	}
	if version < 0 || version > math.MaxInt32 {
		return economics.Snapshot{}, kernel.Invalid("version", "version out of range")
	}
	raw, err := s.queries(ctx).GetSnapshot(ctx, db.GetSnapshotParams{ProductID: product, Period: period, RequestedVersion: int32(version)})
	if err != nil {
		return economics.Snapshot{}, fmt.Errorf("financial snapshot: %w", pgdb.MapError(err))
	}
	return decodeSnapshot(sc, raw)
}

func decodeSnapshot(sc authz.Scope, raw []byte) (economics.Snapshot, error) {
	var snapshot economics.Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return economics.Snapshot{}, fmt.Errorf("decode financial snapshot: %w", err)
	}
	if err := economics.RequireSnapshot(sc, snapshot, authz.ActionReadFinance); err != nil {
		return economics.Snapshot{}, err
	}
	return snapshot, nil
}

func (s *Store) History(ctx context.Context, sc authz.Scope, product kernel.ID, period string) ([]economics.SnapshotInfo, error) {
	if err := sc.Require(authz.ActionReadFinance, product); err != nil {
		return nil, err
	}
	rows, err := s.queries(ctx).ListSnapshots(ctx, db.ListSnapshotsParams{ProductID: product, Period: period})
	if err != nil {
		return nil, fmt.Errorf("financial history: %w", pgdb.MapError(err))
	}
	out := make([]economics.SnapshotInfo, 0, len(rows))
	for _, raw := range rows {
		snapshot, err := decodeSnapshot(sc, raw)
		if err != nil {
			return nil, err
		}
		out = append(out, snapshot.SnapshotInfo)
	}
	return out, nil
}

func (s *Store) Append(ctx context.Context, sc authz.Scope, snapshot economics.Snapshot, expectedVersion int) error {
	if err := economics.RequireSnapshot(sc, snapshot, authz.ActionWriteFinance); err != nil {
		return err
	}
	if expectedVersion < 0 || expectedVersion >= math.MaxInt32 || snapshot.Version < 1 || snapshot.Version > math.MaxInt32 || snapshot.Version != expectedVersion+1 {
		return kernel.ErrConflict
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode financial snapshot: %w", err)
	}
	count, err := s.queries(ctx).AppendSnapshot(ctx, db.AppendSnapshotParams{ID: snapshot.ID, ProductID: snapshot.ProductID, Period: snapshot.Period,
		Version: int32(snapshot.Version), Body: raw, CreatedAt: snapshot.CreatedAt.UTC(), ExpectedVersion: int32(expectedVersion)})
	if err != nil {
		return fmt.Errorf("append financial snapshot: %w", pgdb.MapError(err))
	}
	if count != 1 {
		return kernel.ErrConflict
	}
	return nil
}

func (s *Store) UpsertTemplate(ctx context.Context, sc authz.Scope, template economics.ImportTemplate) error {
	if err := sc.Require(authz.ActionWriteFinance, template.ProductID); err != nil {
		return err
	}
	raw, err := json.Marshal(template)
	if err != nil {
		return fmt.Errorf("encode financial template: %w", err)
	}
	if err := s.queries(ctx).UpsertImportTemplate(ctx, db.UpsertImportTemplateParams{ProductID: template.ProductID, Name: template.Name, Body: raw}); err != nil {
		return fmt.Errorf("save financial template: %w", pgdb.MapError(err))
	}
	return nil
}

func (s *Store) ListTemplates(ctx context.Context, sc authz.Scope, product kernel.ID) ([]economics.ImportTemplate, error) {
	if err := sc.Require(authz.ActionReadFinance, product); err != nil {
		return nil, err
	}
	rows, err := s.queries(ctx).ListImportTemplates(ctx, product)
	if err != nil {
		return nil, fmt.Errorf("list financial templates: %w", pgdb.MapError(err))
	}
	out := make([]economics.ImportTemplate, 0, len(rows))
	for _, raw := range rows {
		var template economics.ImportTemplate
		if err := json.Unmarshal(raw, &template); err != nil {
			return nil, fmt.Errorf("decode financial template: %w", err)
		}
		out = append(out, template)
	}
	return out, nil
}
