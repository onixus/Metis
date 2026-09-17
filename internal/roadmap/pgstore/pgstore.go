// Package pgstore — roadmap.Store на PostgreSQL (схема roadmap). История дат — таблица только для
// INSERT (RM-03, инвариант 8).
package pgstore

import (
	"context"
	"fmt"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/pgdb"
	"github.com/onixus/metis/internal/roadmap"
	"github.com/onixus/metis/internal/roadmap/internal/db"
)

// Store — хранилище элементов, релизов, истории дат и обработанных событий.
type Store struct {
	db *pgdb.DB
}

var _ roadmap.Store = (*Store)(nil)

// New создаёт хранилище на схеме roadmap.
func New(d *pgdb.DB) *Store { return &Store{db: d} }

func (s *Store) q(ctx context.Context) *db.Queries { return db.New(pgdb.Querier(ctx, s.db)) }

// SaveItem создаёт или обновляет элемент.
func (s *Store) SaveItem(ctx context.Context, it roadmap.RoadmapItem) error {
	err := s.q(ctx).UpsertItem(ctx, db.UpsertItemParams{
		ID: it.ID, ProductID: it.ProductID, FeatureID: pgdb.NullID(it.FeatureID), Title: it.Title, Bucket: string(it.Bucket),
		StartDate: pgdb.ToDate(it.StartDate), EndDate: pgdb.ToDate(it.EndDate), ReleaseID: pgdb.NullID(it.ReleaseID),
		Audience: string(it.Audience), Status: string(it.Status), Kind: string(it.Kind), CommitmentID: pgdb.NullID(it.CommitmentID),
		LaunchTier: string(it.LaunchTier), LaunchDate: pgdb.ToDate(it.LaunchDate),
		CreatedAt: it.CreatedAt.UTC(), UpdatedAt: it.UpdatedAt.UTC(),
	})
	if err != nil {
		return fmt.Errorf("roadmap item %s: %w", it.ID, pgdb.MapError(err))
	}
	return nil
}

// Item возвращает элемент по идентификатору.
func (s *Store) Item(ctx context.Context, id kernel.ID) (roadmap.RoadmapItem, error) {
	r, err := s.q(ctx).GetItem(ctx, id)
	if err != nil {
		return roadmap.RoadmapItem{}, fmt.Errorf("roadmap item %s: %w", id, pgdb.MapError(err))
	}
	return itemFromRow(r), nil
}

// Items возвращает элементы продукта в порядке создания.
func (s *Store) Items(ctx context.Context, productID kernel.ID) ([]roadmap.RoadmapItem, error) {
	rows, err := s.q(ctx).ListItemsByProduct(ctx, productID)
	if err != nil {
		return nil, fmt.Errorf("roadmap items %s: %w", productID, pgdb.MapError(err))
	}
	return itemsFromRows(rows), nil
}

// ItemsByFeature возвращает элементы, привязанные к фиче.
func (s *Store) ItemsByFeature(ctx context.Context, featureID kernel.ID) ([]roadmap.RoadmapItem, error) {
	rows, err := s.q(ctx).ListItemsByFeature(ctx, pgdb.NullID(featureID))
	if err != nil {
		return nil, fmt.Errorf("roadmap items by feature %s: %w", featureID, pgdb.MapError(err))
	}
	return itemsFromRows(rows), nil
}

// ItemByCommitment возвращает элемент, созданный по обязательству (CT-04).
func (s *Store) ItemByCommitment(ctx context.Context, commitmentID kernel.ID) (roadmap.RoadmapItem, error) {
	r, err := s.q(ctx).GetItemByCommitment(ctx, pgdb.NullID(commitmentID))
	if err != nil {
		return roadmap.RoadmapItem{}, fmt.Errorf("roadmap item by commitment %s: %w", commitmentID, pgdb.MapError(err))
	}
	return itemFromRow(r), nil
}

func itemsFromRows(rows []db.RoadmapItem) []roadmap.RoadmapItem {
	out := make([]roadmap.RoadmapItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, itemFromRow(r))
	}
	return out
}

func itemFromRow(r db.RoadmapItem) roadmap.RoadmapItem {
	return roadmap.RoadmapItem{
		ID: r.ID, ProductID: r.ProductID, FeatureID: r.FeatureID.UUID, Title: r.Title, Bucket: roadmap.Bucket(r.Bucket),
		StartDate: pgdb.FromDate(r.StartDate), EndDate: pgdb.FromDate(r.EndDate), ReleaseID: r.ReleaseID.UUID,
		Audience: authz.Audience(r.Audience), Status: roadmap.ItemStatus(r.Status), Kind: roadmap.ItemKind(r.Kind),
		CommitmentID: r.CommitmentID.UUID, LaunchTier: roadmap.LaunchTier(r.LaunchTier), LaunchDate: pgdb.FromDate(r.LaunchDate),
		CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}
}

// SaveRelease создаёт или обновляет релиз; матрица совместимости не хранится (вычисляется при чтении).
func (s *Store) SaveRelease(ctx context.Context, r roadmap.Release) error {
	err := s.q(ctx).UpsertRelease(ctx, db.UpsertReleaseParams{
		ID: r.ID, ProductID: r.ProductID, Name: r.Name, Version: r.Version, PlannedDate: pgdb.ToDate(r.PlannedDate),
		Status: string(r.Status), Branch: string(r.Branch), BaseReleaseID: pgdb.NullID(r.BaseReleaseID),
		FeatureIds: pgdb.IDs(r.FeatureIDs), ReleaseNotes: r.ReleaseNotes, Eol: pgdb.ToDate(r.EOL),
		CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	})
	if err != nil {
		return fmt.Errorf("roadmap release %s: %w", r.ID, pgdb.MapError(err))
	}
	return nil
}

// Release возвращает релиз по идентификатору.
func (s *Store) Release(ctx context.Context, id kernel.ID) (roadmap.Release, error) {
	r, err := s.q(ctx).GetRelease(ctx, id)
	if err != nil {
		return roadmap.Release{}, fmt.Errorf("roadmap release %s: %w", id, pgdb.MapError(err))
	}
	return releaseFromRow(r), nil
}

// Releases возвращает релизы продукта в порядке создания.
func (s *Store) Releases(ctx context.Context, productID kernel.ID) ([]roadmap.Release, error) {
	rows, err := s.q(ctx).ListReleasesByProduct(ctx, productID)
	if err != nil {
		return nil, fmt.Errorf("roadmap releases %s: %w", productID, pgdb.MapError(err))
	}
	out := make([]roadmap.Release, 0, len(rows))
	for _, r := range rows {
		out = append(out, releaseFromRow(r))
	}
	return out, nil
}

func releaseFromRow(r db.RoadmapRelease) roadmap.Release {
	return roadmap.Release{
		ID: r.ID, ProductID: r.ProductID, Name: r.Name, Version: r.Version, PlannedDate: pgdb.FromDate(r.PlannedDate),
		Status: roadmap.ReleaseStatus(r.Status), Branch: roadmap.Branch(r.Branch), BaseReleaseID: r.BaseReleaseID.UUID,
		FeatureIDs: pgdb.NilIfEmptyIDs(r.FeatureIds), ReleaseNotes: r.ReleaseNotes, EOL: pgdb.FromDate(r.Eol),
		CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}
}

// AppendDateChange добавляет запись истории дат (только INSERT).
func (s *Store) AppendDateChange(ctx context.Context, ch roadmap.DateChange) error {
	err := s.q(ctx).InsertDateChange(ctx, db.InsertDateChangeParams{
		ID: ch.ID, ItemID: ch.ItemID, ProductID: ch.ProductID,
		OldStart: pgdb.ToDate(ch.OldStart), OldEnd: pgdb.ToDate(ch.OldEnd), NewStart: pgdb.ToDate(ch.NewStart), NewEnd: pgdb.ToDate(ch.NewEnd),
		Reason: ch.Reason, Actor: ch.Actor, At: ch.At.UTC(), EventID: pgdb.NullID(ch.EventID),
	})
	if err != nil {
		return fmt.Errorf("roadmap date change %s: %w", ch.ID, pgdb.MapError(err))
	}
	return nil
}

// DateHistory возвращает историю дат элемента в порядке записи.
func (s *Store) DateHistory(ctx context.Context, itemID kernel.ID) ([]roadmap.DateChange, error) {
	rows, err := s.q(ctx).ListDateHistory(ctx, itemID)
	if err != nil {
		return nil, fmt.Errorf("roadmap date history %s: %w", itemID, pgdb.MapError(err))
	}
	out := make([]roadmap.DateChange, 0, len(rows))
	for _, r := range rows {
		out = append(out, roadmap.DateChange{
			ID: r.ID, ItemID: r.ItemID, ProductID: r.ProductID,
			OldStart: pgdb.FromDate(r.OldStart), OldEnd: pgdb.FromDate(r.OldEnd), NewStart: pgdb.FromDate(r.NewStart), NewEnd: pgdb.FromDate(r.NewEnd),
			Reason: r.Reason, Actor: r.Actor, At: r.At.UTC(), EventID: r.EventID.UUID,
		})
	}
	return out, nil
}

// EventProcessed сообщает, обрабатывалось ли событие.
func (s *Store) EventProcessed(ctx context.Context, eventID kernel.ID) (bool, error) {
	ok, err := s.q(ctx).EventProcessed(ctx, eventID)
	if err != nil {
		return false, fmt.Errorf("roadmap event processed %s: %w", eventID, pgdb.MapError(err))
	}
	return ok, nil
}

// MarkEventProcessed отмечает событие обработанным (идемпотентно).
func (s *Store) MarkEventProcessed(ctx context.Context, eventID kernel.ID) error {
	if err := s.q(ctx).MarkEventProcessed(ctx, eventID); err != nil {
		return fmt.Errorf("roadmap mark event %s: %w", eventID, pgdb.MapError(err))
	}
	return nil
}
