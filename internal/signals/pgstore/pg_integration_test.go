//go:build integration

package pgstore_test

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/migrate"
	"github.com/onixus/metis/internal/kernel/pgdb"
	"github.com/onixus/metis/internal/signals"
	"github.com/onixus/metis/internal/signals/pgstore"
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
	if _, err := db.Pool().Exec(ctx, "TRUNCATE signals.signals"); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestSG01_PGStoreRoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := pgstore.New(db)
	now := time.Date(2026, 9, 17, 10, 30, 0, 123456000, time.UTC)
	product, hyp, feature := kernel.NewID(), kernel.NewID(), kernel.NewID()
	a := signals.Signal{ID: kernel.NewID(), ProductID: product, Source: signals.SourceCRM, Text: "Нужен экспорт в CSV", ExternalKey: "CRM-1",
		AccountID: "acc-1", DealID: "deal-1", Version: "2.0", Segment: "enterprise", Weight: kernel.RUB(100_00), AccountARR: kernel.RUB(1_000_00),
		BlocksDeal: true, Status: signals.StatusNew, DueDate: kernel.DateOf(2026, 10, 1), CreatedBy: "pm", CreatedAt: now, UpdatedAt: now}
	b := signals.Signal{ID: kernel.NewID(), ProductID: product, Source: signals.SourceManual, Text: "Экспорт CSV (дубликат)",
		Status: signals.StatusMerged, MergedInto: a.ID, CreatedBy: "pm", CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)}
	c := signals.Signal{ID: kernel.NewID(), ProductID: product, Source: signals.SourceServiceDesk, Text: "Гипотеза: SSO",
		Status: signals.StatusLinked, HypothesisID: hyp, CreatedBy: "pm", CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second)}
	d := signals.Signal{ID: kernel.NewID(), ProductID: kernel.NewID(), Source: signals.SourceImport, Text: "Другой продукт",
		Status: signals.StatusLinked, FeatureID: feature, CreatedBy: "pm", CreatedAt: now.Add(3 * time.Second), UpdatedAt: now.Add(3 * time.Second)}
	for _, s := range []signals.Signal{a, b, c, d} {
		if err := store.Save(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	a.Text = "Нужен экспорт в CSV и XLSX"
	if err := store.Save(ctx, a); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, a.ID)
	if err != nil || !reflect.DeepEqual(got, a) {
		t.Fatalf("Get:\n got %+v\nwant %+v\nerr=%v", got, a, err)
	}
	if got, err := store.Get(ctx, b.ID); err != nil || got.MergedInto != a.ID || got.Status != signals.StatusMerged {
		t.Fatalf("MergedInto: %+v err=%v", got, err)
	}
	if got, err := store.GetByExternalKey(ctx, "CRM-1"); err != nil || got.ID != a.ID {
		t.Fatalf("GetByExternalKey: %+v err=%v", got, err)
	}
	if _, err := store.GetByExternalKey(ctx, "nope"); !kernel.IsNotFound(err) {
		t.Fatalf("GetByExternalKey nope: %v", err)
	}
	if _, err := store.GetByExternalKey(ctx, ""); !kernel.IsNotFound(err) {
		t.Fatalf("GetByExternalKey empty: %v", err)
	}
	if _, err := store.Get(ctx, kernel.NewID()); !kernel.IsNotFound(err) {
		t.Fatalf("Get missing: %v", err)
	}
	list, err := store.List(ctx, signals.Filter{ProductID: product})
	if err != nil || len(list) != 3 || list[0].ID != a.ID || list[2].ID != c.ID {
		t.Fatalf("List by product: %d err=%v", len(list), err)
	}
	if list, err := store.List(ctx, signals.Filter{HypothesisID: hyp}); err != nil || len(list) != 1 || list[0].ID != c.ID {
		t.Fatalf("List by hypothesis: %+v err=%v", list, err)
	}
	if list, err := store.List(ctx, signals.Filter{FeatureID: feature}); err != nil || len(list) != 1 || list[0].ID != d.ID {
		t.Fatalf("List by feature: %+v err=%v", list, err)
	}
	if list, err := store.List(ctx, signals.Filter{Statuses: []signals.Status{signals.StatusLinked, signals.StatusMerged}}); err != nil || len(list) != 3 {
		t.Fatalf("List by statuses: %d err=%v", len(list), err)
	}
	if list, err := store.List(ctx, signals.Filter{}); err != nil || len(list) != 4 {
		t.Fatalf("List all: %d err=%v", len(list), err)
	}
}
