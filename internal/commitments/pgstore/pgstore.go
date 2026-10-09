// Package pgstore — commitments.Store на PostgreSQL (схема commitments). Алерты — append-only
// таблица: триггер допускает изменение только полей подтверждения (CT-03).
package pgstore

import (
	"context"
	"fmt"

	"github.com/onixus/metis/internal/commitments"
	"github.com/onixus/metis/internal/commitments/internal/db"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/pgdb"
)

// Store — хранилище обязательств, алертов, обработанных событий и настроек.
type Store struct {
	db    *pgdb.DB
	clock kernel.Clock
}

var _ commitments.Store = (*Store)(nil)

// New создаёт хранилище на схеме commitments. clock может быть nil (системные часы).
func New(d *pgdb.DB, clock kernel.Clock) *Store {
	if clock == nil {
		clock = kernel.SystemClock{}
	}
	return &Store{db: d, clock: clock}
}

func (s *Store) q(ctx context.Context) *db.Queries { return db.New(pgdb.Querier(ctx, s.db)) }

// Save создаёт или обновляет обязательство.
func (s *Store) Save(ctx context.Context, sc authz.Scope, c commitments.Commitment) error {
	if c.ProductID == kernel.NilID || sc.Product(c.ProductID) < authz.AccessPrivate || !sc.Allows(authz.ActionWriteCommitments, c.ProductID) {
		return kernel.ErrForbidden
	}
	if !sc.Valid() {
		return kernel.ErrForbidden
	}
	n, err := s.q(ctx).UpsertCommitment(ctx, db.UpsertCommitmentParams{
		ID: c.ID, ProductID: c.ProductID, Kind: string(c.Kind), Subtype: string(c.Subtype), Counterparty: c.Counterparty,
		Subject: c.Subject, DueDate: pgdb.ToDate(c.DueDate), Basis: c.Basis, Owner: c.Owner, Status: string(c.Status),
		FeatureID: pgdb.NullID(c.FeatureID), ReleaseID: pgdb.NullID(c.ReleaseID), RenewalItemID: pgdb.NullID(c.RenewalItemID),
		CreatedBy: c.CreatedBy, CreatedAt: c.CreatedAt.UTC(), UpdatedAt: c.UpdatedAt.UTC(),
	})
	if err != nil {
		return fmt.Errorf("commitments save %s: %w", c.ID, pgdb.MapError(err))
	}
	if n == 0 {
		return kernel.ErrForbidden
	}
	return nil
}

// Get возвращает обязательство.
func (s *Store) Get(ctx context.Context, sc authz.Scope, id kernel.ID) (commitments.Commitment, error) {
	if !sc.Valid() {
		return commitments.Commitment{}, kernel.ErrForbidden
	}
	r, err := s.q(ctx).GetCommitment(ctx, id)
	if err != nil {
		return commitments.Commitment{}, fmt.Errorf("commitments get %s: %w", id, pgdb.MapError(err))
	}
	if !sc.Allows(authz.ActionReadStrategic, r.ProductID) {
		return commitments.Commitment{}, kernel.ErrForbidden
	}
	return fromRow(r), nil
}

// List возвращает обязательства по фильтру в порядке создания.
func (s *Store) List(ctx context.Context, sc authz.Scope, f commitments.Filter) ([]commitments.Commitment, error) {
	if f.ProductID != kernel.NilID && !sc.Allows(authz.ActionReadStrategic, f.ProductID) {
		return nil, kernel.ErrForbidden
	}
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	statuses := make([]string, 0, len(f.Statuses))
	for _, st := range f.Statuses {
		statuses = append(statuses, string(st))
	}
	rows, err := s.q(ctx).ListCommitments(ctx, db.ListCommitmentsParams{
		ProductID: pgdb.NullID(f.ProductID), Kind: string(f.Kind), Subtype: string(f.Subtype),
		FeatureID: pgdb.NullID(f.FeatureID), ReleaseID: pgdb.NullID(f.ReleaseID), DueBefore: pgdb.ToDate(f.DueBefore), Statuses: statuses,
	})
	if err != nil {
		return nil, fmt.Errorf("commitments list: %w", pgdb.MapError(err))
	}
	out := make([]commitments.Commitment, 0, len(rows))
	for _, r := range rows {
		if !sc.Allows(authz.ActionReadStrategic, r.ProductID) {
			continue
		}
		out = append(out, fromRow(r))
	}
	return out, nil
}

func fromRow(r db.CommitmentsCommitment) commitments.Commitment {
	return commitments.Commitment{
		ID: r.ID, ProductID: r.ProductID, Kind: commitments.Kind(r.Kind), Subtype: commitments.Subtype(r.Subtype),
		Counterparty: r.Counterparty, Subject: r.Subject, DueDate: pgdb.FromDate(r.DueDate), Basis: r.Basis, Owner: r.Owner,
		Status: commitments.Status(r.Status), FeatureID: r.FeatureID.UUID, ReleaseID: r.ReleaseID.UUID, RenewalItemID: r.RenewalItemID.UUID,
		CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}
}

// AppendAlert добавляет алерт (только INSERT).
func (s *Store) AppendAlert(ctx context.Context, sc authz.Scope, a commitments.Alert) error {
	c, err := s.Get(ctx, sc, a.CommitmentID)
	if err != nil {
		return err
	}
	if c.ProductID != a.ProductID {
		return kernel.ErrForbidden
	}
	if a.ProductID == kernel.NilID || sc.Product(a.ProductID) < authz.AccessPrivate || !sc.Allows(authz.ActionWriteCommitments, a.ProductID) {
		return kernel.ErrForbidden
	}
	if !sc.Valid() {
		return kernel.ErrForbidden
	}
	err = s.q(ctx).InsertAlert(ctx, db.InsertAlertParams{
		ID: a.ID, CommitmentID: a.CommitmentID, ProductID: a.ProductID, Kind: string(a.Kind), Message: a.Message,
		EventID: pgdb.NullID(a.EventID), NewDate: pgdb.ToDate(a.NewDate), DueDate: pgdb.ToDate(a.DueDate), RaisedAt: a.RaisedAt.UTC(),
		Acknowledged: a.Acknowledged, AcknowledgedBy: a.AcknowledgedBy, AcknowledgedAt: pgdb.ToTime(a.AcknowledgedAt),
	})
	if err != nil {
		return fmt.Errorf("commitments alert %s: %w", a.ID, pgdb.MapError(err))
	}
	return nil
}

// Alert возвращает алерт.
func (s *Store) Alert(ctx context.Context, sc authz.Scope, id kernel.ID) (commitments.Alert, error) {
	if !sc.Valid() {
		return commitments.Alert{}, kernel.ErrForbidden
	}
	r, err := s.q(ctx).GetAlert(ctx, id)
	if err != nil {
		return commitments.Alert{}, fmt.Errorf("commitments alert %s: %w", id, pgdb.MapError(err))
	}
	if !sc.Allows(authz.ActionReadStrategic, r.ProductID) {
		return commitments.Alert{}, kernel.ErrForbidden
	}
	return alertFromRow(r), nil
}

// Alerts возвращает алерты продукта в порядке добавления; onlyOpen — только неподтверждённые.
func (s *Store) Alerts(ctx context.Context, sc authz.Scope, productID kernel.ID, onlyOpen bool) ([]commitments.Alert, error) {
	if productID != kernel.NilID && !sc.Allows(authz.ActionReadStrategic, productID) {
		return nil, kernel.ErrForbidden
	}
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	rows, err := s.q(ctx).ListAlerts(ctx, db.ListAlertsParams{ProductID: productID, OnlyOpen: onlyOpen})
	if err != nil {
		return nil, fmt.Errorf("commitments alerts %s: %w", productID, pgdb.MapError(err))
	}
	out := make([]commitments.Alert, 0, len(rows))
	for _, r := range rows {
		if !sc.Allows(authz.ActionReadStrategic, r.ProductID) {
			continue
		}
		out = append(out, alertFromRow(r))
	}
	return out, nil
}

// AlertByEvent возвращает алерт, поднятый по паре «обязательство + событие» (CT-03).
// Уникальность пары гарантирует частичный индекс alerts_commitment_event_uniq.
func (s *Store) AlertByEvent(ctx context.Context, sc authz.Scope, commitmentID, eventID kernel.ID) (commitments.Alert, error) {
	if !sc.Valid() {
		return commitments.Alert{}, kernel.ErrForbidden
	}
	if eventID == kernel.NilID {
		return commitments.Alert{}, kernel.NotFound("alert", eventID)
	}
	r, err := s.q(ctx).GetAlertByEvent(ctx, db.GetAlertByEventParams{CommitmentID: commitmentID, EventID: pgdb.NullID(eventID)})
	if err != nil {
		return commitments.Alert{}, fmt.Errorf("commitments alert by event %s/%s: %w", commitmentID, eventID, pgdb.MapError(err))
	}
	if !sc.Allows(authz.ActionReadStrategic, r.ProductID) {
		return commitments.Alert{}, kernel.ErrForbidden
	}
	return alertFromRow(r), nil
}

// Acknowledge сохраняет подтверждение алерта — единственное изменяемое поле.
func (s *Store) Acknowledge(ctx context.Context, sc authz.Scope, a commitments.Alert) error {
	old, err := s.Alert(ctx, sc, a.ID)
	if err != nil {
		return err
	}
	if old.ProductID != a.ProductID {
		return kernel.ErrForbidden
	}
	if a.ProductID == kernel.NilID || sc.Product(a.ProductID) < authz.AccessPrivate || !sc.Allows(authz.ActionWriteCommitments, a.ProductID) {
		return kernel.ErrForbidden
	}
	if !sc.Valid() {
		return kernel.ErrForbidden
	}
	n, err := s.q(ctx).AcknowledgeAlert(ctx, db.AcknowledgeAlertParams{
		ID: a.ID, Acknowledged: a.Acknowledged, AcknowledgedBy: a.AcknowledgedBy, AcknowledgedAt: pgdb.ToTime(a.AcknowledgedAt),
	})
	if err != nil {
		return fmt.Errorf("commitments acknowledge %s: %w", a.ID, pgdb.MapError(err))
	}
	if n == 0 {
		return kernel.NotFound("alert", a.ID)
	}
	return nil
}

func alertFromRow(r db.CommitmentsAlert) commitments.Alert {
	return commitments.Alert{
		ID: r.ID, CommitmentID: r.CommitmentID, ProductID: r.ProductID, Kind: commitments.AlertKind(r.Kind), Message: r.Message,
		EventID: r.EventID.UUID, NewDate: pgdb.FromDate(r.NewDate), DueDate: pgdb.FromDate(r.DueDate), RaisedAt: r.RaisedAt.UTC(),
		Acknowledged: r.Acknowledged, AcknowledgedBy: r.AcknowledgedBy, AcknowledgedAt: pgdb.FromTime(r.AcknowledgedAt),
	}
}

// EventProcessed сообщает, обрабатывалось ли событие.
func (s *Store) EventProcessed(ctx context.Context, sc authz.Scope, eventID kernel.ID) (bool, error) {
	if !sc.Valid() || !sc.HasRole(authz.RoleService) {
		return false, kernel.ErrForbidden
	}
	ok, err := s.q(ctx).EventProcessed(ctx, eventID)
	if err != nil {
		return false, fmt.Errorf("commitments event processed %s: %w", eventID, pgdb.MapError(err))
	}
	return ok, nil
}

// MarkEventProcessed отмечает событие обработанным (идемпотентно).
func (s *Store) MarkEventProcessed(ctx context.Context, sc authz.Scope, eventID kernel.ID) error {
	if !sc.Valid() || !sc.HasRole(authz.RoleService) {
		return kernel.ErrForbidden
	}
	if err := s.q(ctx).MarkEventProcessed(ctx, eventID); err != nil {
		return fmt.Errorf("commitments mark event %s: %w", eventID, pgdb.MapError(err))
	}
	return nil
}

// Settings возвращает настройки; без сохранённых — срок упреждения по умолчанию (CT-04).
func (s *Store) Settings(ctx context.Context, sc authz.Scope) (commitments.Settings, error) {
	if !sc.Valid() {
		return commitments.Settings{}, kernel.ErrForbidden
	}
	lead, err := s.q(ctx).GetSettings(ctx)
	if err != nil {
		if kernel.IsNotFound(pgdb.MapError(err)) {
			return commitments.Settings{LeadMonths: commitments.DefaultLeadMonths}, nil
		}
		return commitments.Settings{}, fmt.Errorf("commitments settings: %w", pgdb.MapError(err))
	}
	return commitments.Settings{LeadMonths: int(lead)}, nil
}

// SaveSettings сохраняет настройки (одна строка).
func (s *Store) SaveSettings(ctx context.Context, sc authz.Scope, st commitments.Settings) error {
	if !sc.Allows(authz.ActionAdminSettings, kernel.NilID) {
		return kernel.ErrForbidden
	}
	err := s.q(ctx).UpsertSettings(ctx, db.UpsertSettingsParams{LeadMonths: int64(st.LeadMonths), UpdatedAt: s.clock.Now().UTC()})
	if err != nil {
		return fmt.Errorf("commitments settings: %w", pgdb.MapError(err))
	}
	return nil
}
