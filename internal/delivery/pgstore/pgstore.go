// Package pgstore — delivery.Store на PostgreSQL (схема delivery): проекция трекера, маппинги,
// состояние синхронизации и обработанные события.
package pgstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/onixus/metis/internal/delivery"
	"github.com/onixus/metis/internal/delivery/internal/db"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/pgdb"
	"github.com/onixus/metis/internal/ports"
)

// Store — хранилище проекции трекера.
type Store struct {
	db    *pgdb.DB
	clock kernel.Clock
}

var _ delivery.Store = (*Store)(nil)

// New создаёт хранилище на схеме delivery. clock может быть nil (системные часы).
func New(d *pgdb.DB, clock kernel.Clock) *Store {
	if clock == nil {
		clock = kernel.SystemClock{}
	}
	return &Store{db: d, clock: clock}
}

func (s *Store) q(ctx context.Context) *db.Queries { return db.New(pgdb.Querier(ctx, s.db)) }

// SaveMapping сохраняет привязку фичи к эпику.
func (s *Store) SaveMapping(ctx context.Context, sc authz.Scope, m delivery.Mapping) error {
	if m.ProductID == kernel.NilID || sc.Product(m.ProductID) < authz.AccessPrivate || !sc.Allows(authz.ActionWriteGraph, m.ProductID) {
		return kernel.ErrForbidden
	}
	if !sc.Valid() {
		return kernel.ErrForbidden
	}
	n, err := s.q(ctx).UpsertMapping(ctx, db.UpsertMappingParams{
		FeatureID: m.FeatureID, ProductID: m.ProductID, EpicKey: m.EpicKey, Project: m.Project, CreatedAt: m.CreatedAt.UTC(),
	})
	if err != nil {
		return fmt.Errorf("delivery mapping %s: %w", m.FeatureID, pgdb.MapError(err))
	}
	if n == 0 {
		return kernel.ErrForbidden
	}
	return nil
}

// MappingByFeature возвращает привязку по фиче.
func (s *Store) MappingByFeature(ctx context.Context, sc authz.Scope, featureID kernel.ID) (delivery.Mapping, error) {
	if !sc.Valid() {
		return delivery.Mapping{}, kernel.ErrForbidden
	}
	r, err := s.q(ctx).GetMappingByFeature(ctx, featureID)
	if err != nil {
		return delivery.Mapping{}, fmt.Errorf("delivery mapping %s: %w", featureID, pgdb.MapError(err))
	}
	if !sc.Allows(authz.ActionReadPrivate, r.ProductID) {
		return delivery.Mapping{}, kernel.ErrForbidden
	}
	return mappingFromRow(r), nil
}

// MappingByEpic возвращает привязку по ключу эпика.
func (s *Store) MappingByEpic(ctx context.Context, sc authz.Scope, epicKey string) (delivery.Mapping, error) {
	if !sc.Valid() {
		return delivery.Mapping{}, kernel.ErrForbidden
	}
	r, err := s.q(ctx).GetMappingByEpic(ctx, epicKey)
	if err != nil {
		return delivery.Mapping{}, fmt.Errorf("delivery mapping %s: %w", epicKey, pgdb.MapError(err))
	}
	if !sc.Allows(authz.ActionReadPrivate, r.ProductID) {
		return delivery.Mapping{}, kernel.ErrForbidden
	}
	return mappingFromRow(r), nil
}

// Mappings возвращает все привязки по ключу эпика.
func (s *Store) Mappings(ctx context.Context, sc authz.Scope) ([]delivery.Mapping, error) {
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	rows, err := s.q(ctx).ListMappings(ctx)
	if err != nil {
		return nil, fmt.Errorf("delivery mappings: %w", pgdb.MapError(err))
	}
	out := make([]delivery.Mapping, 0, len(rows))
	for _, r := range rows {
		if !sc.Allows(authz.ActionReadPrivate, r.ProductID) {
			continue
		}
		out = append(out, mappingFromRow(r))
	}
	return out, nil
}

func mappingFromRow(r db.DeliveryMapping) delivery.Mapping {
	return delivery.Mapping{FeatureID: r.FeatureID, ProductID: r.ProductID, EpicKey: r.EpicKey, Project: r.Project, CreatedAt: r.CreatedAt.UTC()}
}

// SaveReleaseMapping сохраняет привязку релиза к версии трекера.
func (s *Store) SaveReleaseMapping(ctx context.Context, sc authz.Scope, m delivery.ReleaseMapping) error {
	if m.ProductID == kernel.NilID || sc.Product(m.ProductID) < authz.AccessPrivate || !sc.Allows(authz.ActionWriteRoadmap, m.ProductID) {
		return kernel.ErrForbidden
	}
	if !sc.Valid() {
		return kernel.ErrForbidden
	}
	n, err := s.q(ctx).UpsertReleaseMapping(ctx, db.UpsertReleaseMappingParams{
		ReleaseID: m.ReleaseID, ProductID: m.ProductID, Project: m.Project, FixVersion: m.FixVersion, CreatedAt: m.CreatedAt.UTC(),
	})
	if err != nil {
		return fmt.Errorf("delivery release mapping %s: %w", m.ReleaseID, pgdb.MapError(err))
	}
	if n == 0 {
		return kernel.ErrForbidden
	}
	return nil
}

// ReleaseMappings возвращает привязки релизов по версии.
func (s *Store) ReleaseMappings(ctx context.Context, sc authz.Scope) ([]delivery.ReleaseMapping, error) {
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	rows, err := s.q(ctx).ListReleaseMappings(ctx)
	if err != nil {
		return nil, fmt.Errorf("delivery release mappings: %w", pgdb.MapError(err))
	}
	out := make([]delivery.ReleaseMapping, 0, len(rows))
	for _, r := range rows {
		if !sc.Allows(authz.ActionReadPrivate, r.ProductID) {
			continue
		}
		out = append(out, delivery.ReleaseMapping{ReleaseID: r.ReleaseID, ProductID: r.ProductID, Project: r.Project, FixVersion: r.FixVersion, CreatedAt: r.CreatedAt.UTC()})
	}
	return out, nil
}

// SaveEpic сохраняет проекцию эпика.
func (s *Store) SaveEpic(ctx context.Context, sc authz.Scope, p delivery.EpicProjection) error {
	if p.ProductID == kernel.NilID || sc.Product(p.ProductID) < authz.AccessPrivate || !sc.Allows(authz.ActionWriteGraph, p.ProductID) {
		return kernel.ErrForbidden
	}
	if !sc.Valid() || !sc.HasRole(authz.RoleService) {
		return kernel.ErrForbidden
	}
	issues, err := marshalIssues(p.Issues)
	if err != nil {
		return fmt.Errorf("delivery epic %s: %w", p.FeatureID, err)
	}
	n, err := s.q(ctx).UpsertEpic(ctx, db.UpsertEpicParams{
		FeatureID: p.FeatureID, ProductID: p.ProductID, EpicKey: p.EpicKey, Summary: p.Summary, Status: p.Status,
		DueDate: pgdb.ToDate(p.DueDate), FixVersions: pgdb.Strings(p.FixVersions), Issues: issues,
		InitialScope: pgdb.Strings(p.InitialScope), FirstSeenAt: pgdb.ToTime(p.FirstSeenAt), SyncedAt: pgdb.ToTime(p.SyncedAt),
		SourceEventID: p.SourceEventID,
	})
	if err != nil {
		return fmt.Errorf("delivery epic %s: %w", p.FeatureID, pgdb.MapError(err))
	}
	if n == 0 {
		return kernel.ErrForbidden
	}
	return nil
}

// EpicByFeature возвращает проекцию эпика фичи.
func (s *Store) EpicByFeature(ctx context.Context, sc authz.Scope, featureID kernel.ID) (delivery.EpicProjection, error) {
	if !sc.Valid() {
		return delivery.EpicProjection{}, kernel.ErrForbidden
	}
	r, err := s.q(ctx).GetEpicByFeature(ctx, featureID)
	if err != nil {
		return delivery.EpicProjection{}, fmt.Errorf("delivery epic %s: %w", featureID, pgdb.MapError(err))
	}
	issues, err := unmarshalIssues(r.Issues)
	if err != nil {
		return delivery.EpicProjection{}, fmt.Errorf("delivery epic %s: %w", featureID, err)
	}
	if !sc.Allows(authz.ActionReadPrivate, r.ProductID) {
		return delivery.EpicProjection{}, kernel.ErrForbidden
	}
	return delivery.EpicProjection{
		FeatureID: r.FeatureID, ProductID: r.ProductID, EpicKey: r.EpicKey, Summary: r.Summary, Status: r.Status,
		DueDate: pgdb.FromDate(r.DueDate), FixVersions: pgdb.NilIfEmptyStrings(r.FixVersions), Issues: issues,
		InitialScope: pgdb.NilIfEmptyStrings(r.InitialScope), FirstSeenAt: pgdb.FromTime(r.FirstSeenAt), SyncedAt: pgdb.FromTime(r.SyncedAt),
		SourceEventID: r.SourceEventID,
	}, nil
}

// SaveSprints заменяет набор спринтов продукта одной транзакцией.
func (s *Store) SaveSprints(ctx context.Context, sc authz.Scope, productID kernel.ID, sprints []delivery.SprintStatus) error {
	for _, row := range sprints {
		if row.ProductID != productID {
			return kernel.ErrForbidden
		}
	}
	if productID == kernel.NilID || !sc.Allows(authz.ActionReadPrivate, productID) {
		return kernel.ErrForbidden
	}
	if !sc.Valid() || !sc.HasRole(authz.RoleService) {
		return kernel.ErrForbidden
	}
	return s.db.Transact(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		if err := q.DeleteSprintsByProduct(ctx, productID); err != nil {
			return fmt.Errorf("delivery sprints %s: %w", productID, pgdb.MapError(err))
		}
		for i, sp := range sprints {
			issues, err := marshalIssues(sp.Issues)
			if err != nil {
				return fmt.Errorf("delivery sprint %s: %w", sp.SprintID, err)
			}
			err = q.InsertSprint(ctx, db.InsertSprintParams{
				ProductID: productID, Position: int64(i), Board: sp.Board, SprintID: sp.SprintID, Name: sp.Name, Goal: sp.Goal,
				State: string(sp.State), StartDate: pgdb.ToDate(sp.StartDate), EndDate: pgdb.ToDate(sp.EndDate), Issues: issues,
				Total: int64(sp.Total), Done: int64(sp.Done), CarriedOver: pgdb.Strings(sp.CarriedOver), SyncedAt: pgdb.ToTime(sp.SyncedAt),
			})
			if err != nil {
				return fmt.Errorf("delivery sprint %s: %w", sp.SprintID, pgdb.MapError(err))
			}
		}
		return nil
	})
}

// Sprints возвращает спринты продукта в порядке сохранения.
func (s *Store) Sprints(ctx context.Context, sc authz.Scope, productID kernel.ID) ([]delivery.SprintStatus, error) {
	if productID == kernel.NilID || !sc.Allows(authz.ActionReadPrivate, productID) {
		return nil, kernel.ErrForbidden
	}
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	rows, err := s.q(ctx).ListSprintsByProduct(ctx, productID)
	if err != nil {
		return nil, fmt.Errorf("delivery sprints %s: %w", productID, pgdb.MapError(err))
	}
	out := make([]delivery.SprintStatus, 0, len(rows))
	for _, r := range rows {
		issues, err := unmarshalIssues(r.Issues)
		if err != nil {
			return nil, fmt.Errorf("delivery sprint %s: %w", r.SprintID, err)
		}
		out = append(out, delivery.SprintStatus{
			ProductID: r.ProductID, Board: r.Board, SprintID: r.SprintID, Name: r.Name, Goal: r.Goal, State: ports.SprintState(r.State),
			StartDate: pgdb.FromDate(r.StartDate), EndDate: pgdb.FromDate(r.EndDate), Issues: issues, Total: int(r.Total), Done: int(r.Done),
			CarriedOver: pgdb.NilIfEmptyStrings(r.CarriedOver), SyncedAt: pgdb.FromTime(r.SyncedAt),
		})
	}
	return out, nil
}

// SaveSyncState сохраняет состояние синхронизации (одна строка).
func (s *Store) SaveSyncState(ctx context.Context, sc authz.Scope, st delivery.SyncState) error {
	if !sc.Valid() || !sc.HasRole(authz.RoleService) {
		return kernel.ErrForbidden
	}
	err := s.q(ctx).UpsertSyncState(ctx, db.UpsertSyncStateParams{
		LastSuccessAt: pgdb.ToTime(st.LastSuccessAt), LastAttemptAt: pgdb.ToTime(st.LastAttemptAt),
		LastError: st.LastError, LagNs: int64(st.Lag), Stale: st.Stale,
	})
	if err != nil {
		return fmt.Errorf("delivery sync state: %w", pgdb.MapError(err))
	}
	return nil
}

// SyncState возвращает состояние синхронизации; пустое, если сверки ещё не было.
func (s *Store) SyncState(ctx context.Context, sc authz.Scope) (delivery.SyncState, error) {
	if !sc.Valid() {
		return delivery.SyncState{}, kernel.ErrForbidden
	}
	r, err := s.q(ctx).GetSyncState(ctx)
	if err != nil {
		if kernel.IsNotFound(pgdb.MapError(err)) {
			return delivery.SyncState{}, nil
		}
		return delivery.SyncState{}, fmt.Errorf("delivery sync state: %w", pgdb.MapError(err))
	}
	return delivery.SyncState{
		LastSuccessAt: pgdb.FromTime(r.LastSuccessAt), LastAttemptAt: pgdb.FromTime(r.LastAttemptAt),
		LastError: r.LastError, Lag: time.Duration(r.LagNs), Stale: r.Stale,
	}, nil
}

// SaveFieldMapping сохраняет маппинг полей трекера (AD-05).
func (s *Store) SaveFieldMapping(ctx context.Context, sc authz.Scope, m delivery.FieldMapping) error {
	if !sc.Allows(authz.ActionManageConnects, kernel.NilID) {
		return kernel.ErrForbidden
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("delivery field mapping: %w", err)
	}
	if err := s.q(ctx).UpsertFieldMapping(ctx, db.UpsertFieldMappingParams{Mapping: raw, UpdatedAt: s.clock.Now().UTC()}); err != nil {
		return fmt.Errorf("delivery field mapping: %w", pgdb.MapError(err))
	}
	return nil
}

// FieldMapping возвращает маппинг; без сохранённого — delivery.DefaultFieldMapping().
func (s *Store) FieldMapping(ctx context.Context, sc authz.Scope) (delivery.FieldMapping, error) {
	if !sc.Valid() {
		return delivery.FieldMapping{}, kernel.ErrForbidden
	}
	raw, err := s.q(ctx).GetFieldMapping(ctx)
	if err != nil {
		if kernel.IsNotFound(pgdb.MapError(err)) {
			return delivery.DefaultFieldMapping(), nil
		}
		return delivery.FieldMapping{}, fmt.Errorf("delivery field mapping: %w", pgdb.MapError(err))
	}
	var m delivery.FieldMapping
	if err := json.Unmarshal(raw, &m); err != nil {
		return delivery.FieldMapping{}, fmt.Errorf("delivery field mapping: %w", err)
	}
	return m, nil
}

// MarkProcessed запоминает внешний ключ события; false — событие уже обрабатывалось.
func (s *Store) MarkProcessed(ctx context.Context, sc authz.Scope, externalID string) (bool, error) {
	if !sc.Valid() || !sc.HasRole(authz.RoleService) {
		return false, kernel.ErrForbidden
	}
	n, err := s.q(ctx).MarkProcessed(ctx, externalID)
	if err != nil {
		return false, fmt.Errorf("delivery mark processed %q: %w", externalID, pgdb.MapError(err))
	}
	return n > 0, nil
}

func marshalIssues(issues []delivery.IssueSnapshot) ([]byte, error) {
	norm := make([]delivery.IssueSnapshot, len(issues))
	for i, it := range issues {
		it.CreatedAt = it.CreatedAt.UTC()
		norm[i] = it
	}
	raw, err := json.Marshal(norm)
	if err != nil {
		return nil, fmt.Errorf("issues: %w", err)
	}
	return raw, nil
}

func unmarshalIssues(raw []byte) ([]delivery.IssueSnapshot, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var issues []delivery.IssueSnapshot
	if err := json.Unmarshal(raw, &issues); err != nil {
		return nil, fmt.Errorf("issues: %w", err)
	}
	if len(issues) == 0 {
		return nil, nil
	}
	for i := range issues {
		issues[i].CreatedAt = issues[i].CreatedAt.UTC()
	}
	return issues, nil
}
