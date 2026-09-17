//go:build integration

package pgstore_test

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/onixus/metis/internal/delivery"
	"github.com/onixus/metis/internal/delivery/pgstore"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/migrate"
	"github.com/onixus/metis/internal/kernel/pgdb"
	"github.com/onixus/metis/internal/portfoliograph"
	"github.com/onixus/metis/internal/ports"
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
	if _, err := db.Pool().Exec(ctx, `TRUNCATE delivery.mappings, delivery.release_mappings, delivery.epics, delivery.sprints,
		delivery.sync_state, delivery.field_mapping, delivery.processed_events`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestDL01_PGStoreMappingsAndEpics(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := pgstore.New(db, nil)
	now := time.Date(2026, 9, 17, 10, 30, 0, 0, time.UTC)
	product, feature, release := kernel.NewID(), kernel.NewID(), kernel.NewID()
	m := delivery.Mapping{FeatureID: feature, ProductID: product, EpicKey: "EDR-10", Project: "EDR", CreatedAt: now}
	if err := store.SaveMapping(ctx, m); err != nil {
		t.Fatal(err)
	}
	m2 := delivery.Mapping{FeatureID: kernel.NewID(), ProductID: product, EpicKey: "EDR-2", Project: "EDR", CreatedAt: now}
	if err := store.SaveMapping(ctx, m2); err != nil {
		t.Fatal(err)
	}
	if got, err := store.MappingByFeature(ctx, feature); err != nil || !reflect.DeepEqual(got, m) {
		t.Fatalf("MappingByFeature: %+v err=%v", got, err)
	}
	if got, err := store.MappingByEpic(ctx, "EDR-10"); err != nil || got.FeatureID != feature {
		t.Fatalf("MappingByEpic: %+v err=%v", got, err)
	}
	if _, err := store.MappingByEpic(ctx, "EDR-404"); !kernel.IsNotFound(err) {
		t.Fatalf("MappingByEpic missing: %v", err)
	}
	if list, err := store.Mappings(ctx); err != nil || len(list) != 2 || list[0].EpicKey != "EDR-10" {
		t.Fatalf("Mappings: %+v err=%v", list, err)
	}
	rm := delivery.ReleaseMapping{ReleaseID: release, ProductID: product, Project: "EDR", FixVersion: "1.0", CreatedAt: now}
	if err := store.SaveReleaseMapping(ctx, rm); err != nil {
		t.Fatal(err)
	}
	if list, err := store.ReleaseMappings(ctx); err != nil || len(list) != 1 || !reflect.DeepEqual(list[0], rm) {
		t.Fatalf("ReleaseMappings: %+v err=%v", list, err)
	}
	epic := delivery.EpicProjection{FeatureID: feature, ProductID: product, EpicKey: "EDR-10", Summary: "Экспорт", Status: "In Progress",
		DueDate: kernel.DateOf(2026, 11, 1), FixVersions: []string{"1.0"},
		Issues:       []delivery.IssueSnapshot{{Key: "EDR-11", Summary: "task", Status: "Done", Done: true, CreatedAt: now}},
		InitialScope: []string{"EDR-11"}, FirstSeenAt: now, SyncedAt: now.Add(time.Minute), SourceEventID: "evt-1"}
	if err := store.SaveEpic(ctx, epic); err != nil {
		t.Fatal(err)
	}
	epic.Status = "Done"
	if err := store.SaveEpic(ctx, epic); err != nil {
		t.Fatal(err)
	}
	if got, err := store.EpicByFeature(ctx, feature); err != nil || !reflect.DeepEqual(got, epic) {
		t.Fatalf("EpicByFeature:\n got %+v\nwant %+v\nerr=%v", got, epic, err)
	}
	if _, err := store.EpicByFeature(ctx, kernel.NewID()); !kernel.IsNotFound(err) {
		t.Fatalf("EpicByFeature missing: %v", err)
	}
	if ok, err := store.MarkProcessed(ctx, "evt-1"); err != nil || !ok {
		t.Fatalf("MarkProcessed first: %v err=%v", ok, err)
	}
	if ok, err := store.MarkProcessed(ctx, "evt-1"); err != nil || ok {
		t.Fatalf("MarkProcessed repeat: %v err=%v", ok, err)
	}
}

func TestDL02_PGStoreSprints(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := pgstore.New(db, nil)
	now := time.Date(2026, 9, 17, 10, 30, 0, 0, time.UTC)
	product := kernel.NewID()
	sprints := []delivery.SprintStatus{
		{ProductID: product, Board: "EDR", SprintID: "1", Name: "S1", Goal: "цель", State: ports.SprintClosed, StartDate: kernel.DateOf(2026, 9, 1),
			EndDate: kernel.DateOf(2026, 9, 14), Issues: []delivery.IssueSnapshot{{Key: "EDR-1", Done: true, CreatedAt: now}}, Total: 1, Done: 1, SyncedAt: now},
		{ProductID: product, Board: "EDR", SprintID: "2", Name: "S2", State: ports.SprintActive, Total: 2, Done: 0, CarriedOver: []string{"EDR-2"}, SyncedAt: now},
	}
	if err := store.SaveSprints(ctx, product, sprints); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Sprints(ctx, product); err != nil || !reflect.DeepEqual(got, sprints) {
		t.Fatalf("Sprints:\n got %+v\nwant %+v\nerr=%v", got, sprints, err)
	}
	if err := store.SaveSprints(ctx, product, sprints[1:]); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Sprints(ctx, product); err != nil || len(got) != 1 || got[0].SprintID != "2" {
		t.Fatalf("Sprints after replace: %+v err=%v", got, err)
	}
	if got, err := store.Sprints(ctx, kernel.NewID()); err != nil || len(got) != 0 {
		t.Fatalf("Sprints unknown product: %+v err=%v", got, err)
	}
}

func TestAD05_PGStoreSyncStateAndFieldMapping(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := pgstore.New(db, kernel.FixedClock{T: time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)})
	now := time.Date(2026, 9, 17, 10, 30, 0, 0, time.UTC)
	if got, err := store.SyncState(ctx); err != nil || !reflect.DeepEqual(got, delivery.SyncState{}) {
		t.Fatalf("SyncState empty: %+v err=%v", got, err)
	}
	st := delivery.SyncState{LastSuccessAt: now, LastAttemptAt: now.Add(time.Minute), LastError: "timeout", Lag: 90 * time.Second, Stale: true}
	if err := store.SaveSyncState(ctx, st); err != nil {
		t.Fatal(err)
	}
	if got, err := store.SyncState(ctx); err != nil || !reflect.DeepEqual(got, st) {
		t.Fatalf("SyncState: %+v err=%v", got, err)
	}
	if got, err := store.FieldMapping(ctx); err != nil || !reflect.DeepEqual(got, delivery.DefaultFieldMapping()) {
		t.Fatalf("FieldMapping default: %+v err=%v", got, err)
	}
	product := kernel.NewID()
	fm := delivery.FieldMapping{Project: "EDR", Boards: map[kernel.ID]string{product: "EDR board"}, EpicIssueType: "Epic",
		FeatureRefField: "customfield_1", DueDateField: "duedate",
		StatusMap: map[string]portfoliograph.FeatureStatus{"Готово": portfoliograph.FeatureDone}}
	if err := store.SaveFieldMapping(ctx, fm); err != nil {
		t.Fatal(err)
	}
	if got, err := store.FieldMapping(ctx); err != nil || !reflect.DeepEqual(got, fm) {
		t.Fatalf("FieldMapping:\n got %+v\nwant %+v\nerr=%v", got, fm, err)
	}
}
