// Package pgstore — signals.Store на PostgreSQL (схема signals). Вынесен из пакета signals,
// чтобы домен не зависел от pgx (инвариант 2, depguard).
package pgstore

import (
	"context"
	"fmt"

	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/pgdb"
	"github.com/onixus/metis/internal/signals"
	"github.com/onixus/metis/internal/signals/internal/db"
)

// Store — хранилище сигналов.
type Store struct {
	db *pgdb.DB
}

var _ signals.Store = (*Store)(nil)

// New создаёт хранилище на схеме signals.
func New(d *pgdb.DB) *Store { return &Store{db: d} }

func (s *Store) q(ctx context.Context) *db.Queries { return db.New(pgdb.Querier(ctx, s.db)) }

// Save создаёт или обновляет сигнал (upsert по ID).
func (s *Store) Save(ctx context.Context, sg signals.Signal) error {
	err := s.q(ctx).UpsertSignal(ctx, db.UpsertSignalParams{
		ID: sg.ID, ProductID: sg.ProductID, Source: string(sg.Source), Text: sg.Text, ExternalKey: sg.ExternalKey,
		AccountID: sg.AccountID, DealID: sg.DealID, Version: sg.Version, Segment: sg.Segment,
		WeightAmount: sg.Weight.Amount, WeightCurrency: sg.Weight.Currency,
		AccountArrAmount: sg.AccountARR.Amount, AccountArrCurrency: sg.AccountARR.Currency,
		BlocksDeal: sg.BlocksDeal, Status: string(sg.Status), DueDate: pgdb.ToDate(sg.DueDate),
		FeatureID: pgdb.NullID(sg.FeatureID), ContractID: pgdb.NullID(sg.ContractID),
		HypothesisID: pgdb.NullID(sg.HypothesisID), MergedInto: pgdb.NullID(sg.MergedInto),
		CreatedBy: sg.CreatedBy, CreatedAt: sg.CreatedAt.UTC(), UpdatedAt: sg.UpdatedAt.UTC(),
	})
	if err != nil {
		return fmt.Errorf("signals save %s: %w", sg.ID, pgdb.MapError(err))
	}
	return nil
}

// Get возвращает сигнал по идентификатору.
func (s *Store) Get(ctx context.Context, id kernel.ID) (signals.Signal, error) {
	row, err := s.q(ctx).GetSignal(ctx, id)
	if err != nil {
		return signals.Signal{}, fmt.Errorf("signals get %s: %w", id, pgdb.MapError(err))
	}
	return fromRow(row), nil
}

// GetByExternalKey ищет сигнал по ключу внешней системы; kernel.ErrNotFound, если его нет.
func (s *Store) GetByExternalKey(ctx context.Context, key string) (signals.Signal, error) {
	if key == "" {
		return signals.Signal{}, kernel.ErrNotFound
	}
	row, err := s.q(ctx).GetSignalByExternalKey(ctx, key)
	if err != nil {
		return signals.Signal{}, fmt.Errorf("signals by external key %q: %w", key, pgdb.MapError(err))
	}
	return fromRow(row), nil
}

// List возвращает сигналы по фильтру в порядке создания.
func (s *Store) List(ctx context.Context, f signals.Filter) ([]signals.Signal, error) {
	statuses := make([]string, 0, len(f.Statuses))
	for _, st := range f.Statuses {
		statuses = append(statuses, string(st))
	}
	rows, err := s.q(ctx).ListSignals(ctx, db.ListSignalsParams{
		ProductID: pgdb.NullID(f.ProductID), FeatureID: pgdb.NullID(f.FeatureID), ContractID: pgdb.NullID(f.ContractID),
		HypothesisID: pgdb.NullID(f.HypothesisID), Statuses: statuses,
	})
	if err != nil {
		return nil, fmt.Errorf("signals list: %w", pgdb.MapError(err))
	}
	out := make([]signals.Signal, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromRow(r))
	}
	return out, nil
}

func fromRow(r db.SignalsSignal) signals.Signal {
	return signals.Signal{
		ID: r.ID, ProductID: r.ProductID, Source: signals.Source(r.Source), Text: r.Text, ExternalKey: r.ExternalKey,
		AccountID: r.AccountID, DealID: r.DealID, Version: r.Version, Segment: r.Segment,
		Weight:     kernel.Money{Amount: r.WeightAmount, Currency: r.WeightCurrency},
		AccountARR: kernel.Money{Amount: r.AccountArrAmount, Currency: r.AccountArrCurrency},
		BlocksDeal: r.BlocksDeal, Status: signals.Status(r.Status), DueDate: pgdb.FromDate(r.DueDate),
		FeatureID: r.FeatureID.UUID, ContractID: r.ContractID.UUID, HypothesisID: r.HypothesisID.UUID, MergedInto: r.MergedInto.UUID,
		CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}
}
