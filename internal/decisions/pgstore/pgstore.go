// Package pgstore — decisions.Store на PostgreSQL (схема decisions).
package pgstore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/onixus/metis/internal/decisions"
	"github.com/onixus/metis/internal/decisions/internal/db"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/pgdb"
)

// Store — хранилище решений и обработанных событий.
type Store struct {
	db *pgdb.DB
}

var _ decisions.Store = (*Store)(nil)

// New создаёт хранилище на схеме decisions.
func New(d *pgdb.DB) *Store { return &Store{db: d} }

func (s *Store) q(ctx context.Context) *db.Queries { return db.New(pgdb.Querier(ctx, s.db)) }

// Save создаёт или обновляет решение. ProductID == NilID хранится как NULL (портфельное решение).
func (s *Store) Save(ctx context.Context, r decisions.DecisionRecord) error {
	var snapshot []byte
	if r.Snapshot != nil {
		raw, err := json.Marshal(r.Snapshot)
		if err != nil {
			return fmt.Errorf("decisions save %s: snapshot: %w", r.ID, err)
		}
		snapshot = raw
	}
	options := r.Options
	if options == nil {
		options = []decisions.Option{}
	}
	rawOptions, err := json.Marshal(options)
	if err != nil {
		return fmt.Errorf("decisions save %s: options: %w", r.ID, err)
	}
	links := r.Links
	if links == nil {
		links = []decisions.Link{}
	}
	rawLinks, err := json.Marshal(links)
	if err != nil {
		return fmt.Errorf("decisions save %s: links: %w", r.ID, err)
	}
	err = s.q(ctx).UpsertRecord(ctx, db.UpsertRecordParams{
		ID: r.ID, ProductID: pgdb.NullID(r.ProductID), Title: r.Title, Context: r.Context, Snapshot: snapshot, Options: rawOptions,
		ChosenKey: r.ChosenKey, Rationale: r.Rationale, ExpectedEffect: r.ExpectedEffect, ReviewDate: pgdb.ToDate(r.ReviewDate),
		Status: string(r.Status), SupersededBy: pgdb.NullID(r.SupersededBy), Links: rawLinks, PageID: r.PageID, Author: r.Author,
		CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	})
	if err != nil {
		return fmt.Errorf("decisions save %s: %w", r.ID, pgdb.MapError(err))
	}
	return nil
}

// Get возвращает решение.
func (s *Store) Get(ctx context.Context, id kernel.ID) (decisions.DecisionRecord, error) {
	r, err := s.q(ctx).GetRecord(ctx, id)
	if err != nil {
		return decisions.DecisionRecord{}, fmt.Errorf("decisions get %s: %w", id, pgdb.MapError(err))
	}
	return fromRow(r)
}

// List возвращает решения по фильтру в порядке создания.
func (s *Store) List(ctx context.Context, f decisions.Filter) ([]decisions.DecisionRecord, error) {
	var link []byte
	if f.Link != nil {
		raw, err := json.Marshal([]decisions.Link{*f.Link})
		if err != nil {
			return nil, fmt.Errorf("decisions list: link: %w", err)
		}
		link = raw
	}
	rows, err := s.q(ctx).ListRecords(ctx, db.ListRecordsParams{
		HasProduct: f.HasProduct, ProductID: pgdb.NullID(f.ProductID), Status: string(f.Status), Link: link,
	})
	if err != nil {
		return nil, fmt.Errorf("decisions list: %w", pgdb.MapError(err))
	}
	out := make([]decisions.DecisionRecord, 0, len(rows))
	for _, r := range rows {
		rec, err := fromRow(r)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

func fromRow(r db.DecisionsRecord) (decisions.DecisionRecord, error) {
	rec := decisions.DecisionRecord{
		ID: r.ID, ProductID: r.ProductID.UUID, Title: r.Title, Context: r.Context, ChosenKey: r.ChosenKey, Rationale: r.Rationale,
		ExpectedEffect: r.ExpectedEffect, ReviewDate: pgdb.FromDate(r.ReviewDate), Status: decisions.Status(r.Status),
		SupersededBy: r.SupersededBy.UUID, PageID: r.PageID, Author: r.Author, CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}
	if len(r.Snapshot) > 0 && string(r.Snapshot) != "null" {
		if err := json.Unmarshal(r.Snapshot, &rec.Snapshot); err != nil {
			return decisions.DecisionRecord{}, fmt.Errorf("decisions record %s: snapshot: %w", r.ID, err)
		}
	}
	if len(r.Options) > 0 {
		if err := json.Unmarshal(r.Options, &rec.Options); err != nil {
			return decisions.DecisionRecord{}, fmt.Errorf("decisions record %s: options: %w", r.ID, err)
		}
	}
	if len(r.Links) > 0 {
		if err := json.Unmarshal(r.Links, &rec.Links); err != nil {
			return decisions.DecisionRecord{}, fmt.Errorf("decisions record %s: links: %w", r.ID, err)
		}
	}
	if len(rec.Links) == 0 {
		rec.Links = nil
	}
	return rec, nil
}

// EventProcessed сообщает, обрабатывалось ли событие.
func (s *Store) EventProcessed(ctx context.Context, eventID kernel.ID) (bool, error) {
	ok, err := s.q(ctx).EventProcessed(ctx, eventID)
	if err != nil {
		return false, fmt.Errorf("decisions event processed %s: %w", eventID, pgdb.MapError(err))
	}
	return ok, nil
}

// MarkEventProcessed отмечает событие обработанным (идемпотентно).
func (s *Store) MarkEventProcessed(ctx context.Context, eventID kernel.ID) error {
	if err := s.q(ctx).MarkEventProcessed(ctx, eventID); err != nil {
		return fmt.Errorf("decisions mark event %s: %w", eventID, pgdb.MapError(err))
	}
	return nil
}
