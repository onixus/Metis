//go:build integration

package pgstore_test

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/migrate"
	"github.com/onixus/metis/internal/kernel/pgdb"
	"github.com/onixus/metis/internal/roadmap"
	"github.com/onixus/metis/internal/roadmap/pgstore"
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
	// TRUNCATE не проходит через триггер строк; журнал тестовой БД чистится от владельца.
	if _, err := db.Pool().Exec(ctx, "TRUNCATE roadmap.items, roadmap.releases, roadmap.date_history, roadmap.processed_events"); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestRM01_PGStoreItemsAndReleases(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := pgstore.New(db)
	now := time.Date(2026, 9, 17, 10, 30, 0, 0, time.UTC)
	product, feature, commitment := kernel.NewID(), kernel.NewID(), kernel.NewID()
	base := roadmap.Release{ID: kernel.NewID(), ProductID: product, Name: "R1", Version: "1.0", PlannedDate: kernel.DateOf(2026, 12, 1),
		Status: roadmap.ReleasePlanned, Branch: roadmap.BranchEvolving, FeatureIDs: []kernel.ID{feature}, ReleaseNotes: "notes",
		EOL: kernel.DateOf(2031, 12, 1), CreatedAt: now, UpdatedAt: now}
	cert := roadmap.Release{ID: kernel.NewID(), ProductID: product, Name: "R1-cert", Version: "1.0.1", Status: roadmap.ReleaseReadyForCertification,
		Branch: roadmap.BranchCertified, BaseReleaseID: base.ID, CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)}
	for _, r := range []roadmap.Release{base, cert} {
		if err := store.SaveRelease(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	base.CompatibilityMatrix = []roadmap.CompatRow{{ContractID: kernel.NewID()}}
	if err := store.SaveRelease(ctx, base); err != nil {
		t.Fatal(err)
	}
	base.CompatibilityMatrix = nil // вычисляемое поле не хранится
	if got, err := store.Release(ctx, base.ID); err != nil || !reflect.DeepEqual(got, base) {
		t.Fatalf("Release:\n got %+v\nwant %+v\nerr=%v", got, base, err)
	}
	if got, err := store.Release(ctx, cert.ID); err != nil || !reflect.DeepEqual(got, cert) {
		t.Fatalf("Release cert:\n got %+v\nwant %+v\nerr=%v", got, cert, err)
	}
	if list, err := store.Releases(ctx, product); err != nil || len(list) != 2 || list[0].ID != base.ID {
		t.Fatalf("Releases: %+v err=%v", list, err)
	}
	it := roadmap.RoadmapItem{ID: kernel.NewID(), ProductID: product, FeatureID: feature, Title: "Экспорт", Bucket: roadmap.BucketNow,
		StartDate: kernel.DateOf(2026, 10, 1), EndDate: kernel.DateOf(2026, 11, 1), ReleaseID: base.ID, Audience: authz.AudienceInternal,
		Status: roadmap.ItemPlanned, Kind: roadmap.KindFeature, CreatedAt: now, UpdatedAt: now}
	fix := roadmap.RoadmapItem{ID: kernel.NewID(), ProductID: product, Title: "Исправление", Bucket: roadmap.BucketNext, ReleaseID: cert.ID,
		Audience: authz.AudienceSalesSafe, Status: roadmap.ItemPlanned, Kind: roadmap.KindFix, CommitmentID: commitment,
		CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)}
	for _, i := range []roadmap.RoadmapItem{it, fix} {
		if err := store.SaveItem(ctx, i); err != nil {
			t.Fatal(err)
		}
	}
	it.Title = "Экспорт CSV"
	if err := store.SaveItem(ctx, it); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Item(ctx, it.ID); err != nil || !reflect.DeepEqual(got, it) {
		t.Fatalf("Item:\n got %+v\nwant %+v\nerr=%v", got, it, err)
	}
	if list, err := store.Items(ctx, product); err != nil || len(list) != 2 || list[0].ID != it.ID {
		t.Fatalf("Items: %+v err=%v", list, err)
	}
	if list, err := store.ItemsByFeature(ctx, feature); err != nil || len(list) != 1 || list[0].ID != it.ID {
		t.Fatalf("ItemsByFeature: %+v err=%v", list, err)
	}
	if got, err := store.ItemByCommitment(ctx, commitment); err != nil || !reflect.DeepEqual(got, fix) {
		t.Fatalf("ItemByCommitment: %+v err=%v", got, err)
	}
	if _, err := store.ItemByCommitment(ctx, kernel.NewID()); !kernel.IsNotFound(err) {
		t.Fatalf("ItemByCommitment missing: %v", err)
	}
	if _, err := store.Item(ctx, kernel.NewID()); !kernel.IsNotFound(err) {
		t.Fatalf("Item missing: %v", err)
	}
}

func TestRM03_PGDateHistoryAppendOnly(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := pgstore.New(db)
	now := time.Date(2026, 9, 17, 10, 30, 0, 0, time.UTC)
	item, product, event := kernel.NewID(), kernel.NewID(), kernel.NewID()
	changes := []roadmap.DateChange{
		{ID: kernel.NewID(), ItemID: item, ProductID: product, OldStart: kernel.DateOf(2026, 10, 1), OldEnd: kernel.DateOf(2026, 11, 1),
			NewStart: kernel.DateOf(2026, 10, 15), NewEnd: kernel.DateOf(2026, 11, 15), Reason: "сдвиг", Actor: "pm", At: now},
		{ID: kernel.NewID(), ItemID: item, ProductID: product, NewStart: kernel.DateOf(2026, 12, 1), NewEnd: kernel.DateOf(2026, 12, 20),
			Reason: "затронуто сдвигом фичи X", Actor: "system", At: now.Add(time.Second), EventID: event},
	}
	err := db.Transact(ctx, func(ctx context.Context) error {
		for _, ch := range changes {
			if err := store.AppendDateChange(ctx, ch); err != nil {
				return err
			}
		}
		return store.MarkEventProcessed(ctx, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := store.DateHistory(ctx, item); err != nil || !reflect.DeepEqual(got, changes) {
		t.Fatalf("DateHistory:\n got %+v\nwant %+v\nerr=%v", got, changes, err)
	}
	if _, err := db.Pool().Exec(ctx, "UPDATE roadmap.date_history SET reason = 'x' WHERE id = $1", changes[0].ID); err == nil {
		t.Fatal("UPDATE roadmap.date_history должен быть отклонён триггером")
	}
	if _, err := db.Pool().Exec(ctx, "DELETE FROM roadmap.date_history WHERE id = $1", changes[0].ID); err == nil {
		t.Fatal("DELETE FROM roadmap.date_history должен быть отклонён триггером")
	}
	if ok, err := store.EventProcessed(ctx, event); err != nil || !ok {
		t.Fatalf("EventProcessed: %v err=%v", ok, err)
	}
	if ok, err := store.EventProcessed(ctx, kernel.NewID()); err != nil || ok {
		t.Fatalf("EventProcessed unknown: %v err=%v", ok, err)
	}
	if err := store.MarkEventProcessed(ctx, event); err != nil {
		t.Fatalf("повторная отметка события должна быть идемпотентной: %v", err)
	}
}
