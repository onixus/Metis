//go:build integration

package pgstore_test

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/onixus/metis/internal/commitments"
	"github.com/onixus/metis/internal/commitments/pgstore"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/migrate"
	"github.com/onixus/metis/internal/kernel/pgdb"
)

func openTestDB(t *testing.T) *pgdb.DB {
	t.Helper()
	url := os.Getenv("METIS_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("METIS_TEST_DATABASE_URL не задан: интеграционный тест пропущен (docs/questions.md №05)")
	}
	ctx := context.Background()
	db, err := pgdb.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := migrate.Up(ctx, db.Pool(), nil); err != nil {
		t.Fatal(err)
	}
	// TRUNCATE не проходит через триггер строк; список тестовой БД чистится от владельца.
	if _, err := db.Pool().Exec(ctx, "TRUNCATE commitments.commitments, commitments.alerts, commitments.processed_events, commitments.settings"); err != nil {
		t.Fatal(err)
	}
	return db
}

var now = time.Date(2026, 9, 17, 10, 30, 0, 0, time.UTC)

func TestCT01_PGStoreRoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := pgstore.New(db, nil)
	product, feature, release := kernel.NewID(), kernel.NewID(), kernel.NewID()
	cust := commitments.Commitment{ID: kernel.NewID(), ProductID: product, Kind: commitments.KindCustomer, Counterparty: "acc-1", Subject: "Экспорт CSV",
		DueDate: kernel.DateOf(2026, 12, 1), Basis: "договор 1", Owner: "pm", Status: commitments.StatusActive, FeatureID: feature, CreatedBy: "pm", CreatedAt: now, UpdatedAt: now}
	reg := commitments.Commitment{ID: kernel.NewID(), ProductID: product, Kind: commitments.KindRegulatory, Subtype: commitments.SubtypeCertificateExpiry,
		Counterparty: "регулятор", Subject: "Сертификат", DueDate: kernel.DateOf(2028, 3, 1), Basis: "сертификат №1", Owner: "compliance",
		Status: commitments.StatusActive, ReleaseID: release, RenewalItemID: kernel.NewID(), CreatedBy: "compliance", CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)}
	for _, c := range []commitments.Commitment{cust, reg} {
		if err := store.Save(ctx, repositoryScope(), c); err != nil {
			t.Fatal(err)
		}
	}
	cust.Status = commitments.StatusFulfilled
	if err := store.Save(ctx, repositoryScope(), cust); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Get(ctx, repositoryScope(), cust.ID); err != nil || !reflect.DeepEqual(got, cust) {
		t.Fatalf("Get:\n got %+v\nwant %+v\nerr=%v", got, cust, err)
	}
	if got, err := store.Get(ctx, repositoryScope(), reg.ID); err != nil || !reflect.DeepEqual(got, reg) {
		t.Fatalf("Get reg:\n got %+v\nwant %+v\nerr=%v", got, reg, err)
	}
	cases := map[string]struct {
		f    commitments.Filter
		want int
	}{
		"product":   {commitments.Filter{ProductID: product}, 2},
		"kind":      {commitments.Filter{Kind: commitments.KindRegulatory}, 1},
		"subtype":   {commitments.Filter{Subtype: commitments.SubtypeSupportEnd}, 0},
		"feature":   {commitments.Filter{FeatureID: feature}, 1},
		"release":   {commitments.Filter{ReleaseID: release}, 1},
		"due":       {commitments.Filter{DueBefore: kernel.DateOf(2026, 12, 1)}, 1},
		"statuses":  {commitments.Filter{Statuses: []commitments.Status{commitments.StatusActive}}, 1},
		"all":       {commitments.Filter{}, 2},
		"other prd": {commitments.Filter{ProductID: kernel.NewID()}, 0},
	}
	for name, c := range cases {
		list, err := store.List(ctx, repositoryScope(), c.f)
		if err != nil || len(list) != c.want {
			t.Fatalf("List %s: %d err=%v", name, len(list), err)
		}
	}
	if _, err := store.Get(ctx, repositoryScope(), kernel.NewID()); !kernel.IsNotFound(err) {
		t.Fatalf("Get missing: %v", err)
	}
}

func TestCT03_PGAlertsAppendOnly(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := pgstore.New(db, nil)
	product, event := kernel.NewID(), kernel.NewID()
	a := commitments.Alert{ID: kernel.NewID(), CommitmentID: kernel.NewID(), ProductID: product, Kind: commitments.AlertRoadmapShift, Message: "срыв срока",
		EventID: event, NewDate: kernel.DateOf(2026, 12, 15), DueDate: kernel.DateOf(2026, 12, 1), RaisedAt: now}
	b := commitments.Alert{ID: kernel.NewID(), CommitmentID: a.CommitmentID, ProductID: product, Kind: commitments.AlertRoadmapShift, Message: "ещё раз", RaisedAt: now.Add(time.Second)}
	if err := store.Save(ctx, repositoryScope(), commitments.Commitment{ID: a.CommitmentID, ProductID: product, Kind: commitments.KindRegulatory, Subject: "Synthetic obligation", Status: commitments.StatusActive, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	err := db.Transact(ctx, func(ctx context.Context) error {
		for _, x := range []commitments.Alert{a, b} {
			if err := store.AppendAlert(ctx, repositoryScope(), x); err != nil {
				return err
			}
		}
		return store.MarkEventProcessed(ctx, repositoryScope(), event)
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := store.Alert(ctx, repositoryScope(), a.ID); err != nil || !reflect.DeepEqual(got, a) {
		t.Fatalf("Alert:\n got %+v\nwant %+v\nerr=%v", got, a, err)
	}
	a.Acknowledged, a.AcknowledgedBy, a.AcknowledgedAt = true, "pm", now.Add(time.Hour)
	if err := store.Acknowledge(ctx, repositoryScope(), a); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Alert(ctx, repositoryScope(), a.ID); err != nil || !reflect.DeepEqual(got, a) {
		t.Fatalf("Alert after ack:\n got %+v\nwant %+v\nerr=%v", got, a, err)
	}
	if list, err := store.Alerts(ctx, repositoryScope(), product, false); err != nil || len(list) != 2 || list[0].ID != a.ID {
		t.Fatalf("Alerts all: %+v err=%v", list, err)
	}
	if list, err := store.Alerts(ctx, repositoryScope(), product, true); err != nil || len(list) != 1 || list[0].ID != b.ID {
		t.Fatalf("Alerts open: %+v err=%v", list, err)
	}
	if err := store.Acknowledge(ctx, repositoryScope(), commitments.Alert{ID: kernel.NewID(), Acknowledged: true}); !kernel.IsNotFound(err) {
		t.Fatalf("Acknowledge missing: %v", err)
	}
	if _, err := db.Pool().Exec(ctx, "UPDATE commitments.alerts SET message = 'x' WHERE id = $1", a.ID); err == nil {
		t.Fatal("изменение содержимого алерта должно быть отклонено триггером")
	}
	if _, err := db.Pool().Exec(ctx, "DELETE FROM commitments.alerts WHERE id = $1", a.ID); err == nil {
		t.Fatal("DELETE алерта должен быть отклонён триггером")
	}
	if ok, err := store.EventProcessed(ctx, repositoryScope(), event); err != nil || !ok {
		t.Fatalf("EventProcessed: %v err=%v", ok, err)
	}
	if ok, err := store.EventProcessed(ctx, repositoryScope(), kernel.NewID()); err != nil || ok {
		t.Fatalf("EventProcessed unknown: %v err=%v", ok, err)
	}
}

func TestCT04_PGSettings(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := pgstore.New(db, kernel.FixedClock{T: now})
	if got, err := store.Settings(ctx, repositoryScope()); err != nil || got.LeadMonths != commitments.DefaultLeadMonths {
		t.Fatalf("Settings default: %+v err=%v", got, err)
	}
	if err := store.SaveSettings(ctx, repositoryScope(), commitments.Settings{LeadMonths: 12}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSettings(ctx, repositoryScope(), commitments.Settings{LeadMonths: 24}); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Settings(ctx, repositoryScope()); err != nil || got.LeadMonths != 24 {
		t.Fatalf("Settings: %+v err=%v", got, err)
	}
}

// TestCT03_PGAlertUniquePerEvent — частичный уникальный индекс (commitment_id, event_id):
// второй алерт по той же паре отклоняется как kernel.ErrConflict, алерты без события не ограничены;
// AlertByEvent находит записанный алерт (CT-03).
func TestCT03_PGAlertUniquePerEvent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := pgstore.New(db, nil)
	product, commitment, event := kernel.NewID(), kernel.NewID(), kernel.NewID()
	a := commitments.Alert{ID: kernel.NewID(), CommitmentID: commitment, ProductID: product, Kind: commitments.AlertRoadmapShift,
		Message: "срыв срока", EventID: event, NewDate: kernel.DateOf(2026, 12, 15), DueDate: kernel.DateOf(2026, 12, 1), RaisedAt: now}
	if err := store.Save(ctx, repositoryScope(), commitments.Commitment{ID: a.CommitmentID, ProductID: product, Kind: commitments.KindRegulatory, Subject: "Synthetic obligation", Status: commitments.StatusActive, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	if err := store.AppendAlert(ctx, repositoryScope(), a); err != nil {
		t.Fatal(err)
	}
	dup := a
	dup.ID, dup.Message = kernel.NewID(), "повтор доставки события"
	if err := store.AppendAlert(ctx, repositoryScope(), dup); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("дубль по паре обязательство+событие: ожидался ErrConflict, получено %v", err)
	}
	// Другое обязательство и другое событие — не дубли.
	other := a
	other.ID, other.CommitmentID = kernel.NewID(), kernel.NewID()
	if err := store.Save(ctx, repositoryScope(), commitments.Commitment{ID: other.CommitmentID, ProductID: product, Kind: commitments.KindRegulatory, Subject: "Synthetic second obligation", Status: commitments.StatusActive, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendAlert(ctx, repositoryScope(), other); err != nil {
		t.Fatalf("другое обязательство: %v", err)
	}
	otherEvent := a
	otherEvent.ID, otherEvent.EventID = kernel.NewID(), kernel.NewID()
	if err := store.AppendAlert(ctx, repositoryScope(), otherEvent); err != nil {
		t.Fatalf("другое событие: %v", err)
	}
	// Алерты без события индексом не ограничены.
	for i := 0; i < 2; i++ {
		manual := commitments.Alert{ID: kernel.NewID(), CommitmentID: commitment, ProductID: product,
			Kind: commitments.AlertRoadmapShift, Message: "вручную", RaisedAt: now}
		if err := store.AppendAlert(ctx, repositoryScope(), manual); err != nil {
			t.Fatalf("алерт без события #%d: %v", i, err)
		}
	}

	got, err := store.AlertByEvent(ctx, repositoryScope(), commitment, event)
	if err != nil || !reflect.DeepEqual(got, a) {
		t.Fatalf("AlertByEvent:\n got %+v\nwant %+v\nerr=%v", got, a, err)
	}
	if _, err := store.AlertByEvent(ctx, repositoryScope(), commitment, kernel.NewID()); !kernel.IsNotFound(err) {
		t.Fatalf("AlertByEvent неизвестного события: %v", err)
	}
	if _, err := store.AlertByEvent(ctx, repositoryScope(), kernel.NewID(), event); !kernel.IsNotFound(err) {
		t.Fatalf("AlertByEvent чужого обязательства: %v", err)
	}
	if _, err := store.AlertByEvent(ctx, repositoryScope(), commitment, kernel.NilID); !kernel.IsNotFound(err) {
		t.Fatalf("AlertByEvent без события: %v", err)
	}
}

func repositoryScope() authz.Scope {
	return authz.New(authz.Params{Subject: "repository-fixture", Roles: []authz.Role{authz.RoleAdmin, authz.RoleService}, AllProducts: authz.AccessPrivate, Audience: authz.AudienceInternal})
}
