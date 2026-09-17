//go:build integration

package pgstore_test

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/onixus/metis/internal/decisions"
	"github.com/onixus/metis/internal/decisions/pgstore"
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
	if _, err := db.Pool().Exec(ctx, "TRUNCATE decisions.records, decisions.processed_events"); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestDA01_PGStoreRoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := pgstore.New(db)
	now := time.Date(2026, 9, 17, 10, 30, 0, 0, time.UTC)
	product, hyp, feature := kernel.NewID(), kernel.NewID(), kernel.NewID()
	rec := decisions.DecisionRecord{ID: kernel.NewID(), ProductID: product, Title: "Выбор протокола", Context: "контекст",
		Snapshot:  map[string]any{"arr": "1000.00 RUB", "features": float64(3)},
		Options:   []decisions.Option{{Key: "a", Title: "A", Description: "…"}, {Key: "b", Title: "B"}},
		ChosenKey: "a", Rationale: "быстрее", ExpectedEffect: "+ARR", ReviewDate: kernel.DateOf(2027, 3, 1), Status: decisions.StatusProposed,
		Links:  []decisions.Link{{Kind: decisions.LinkHypothesis, ID: hyp}, {Kind: decisions.LinkFeature, ID: feature}},
		Author: "cpo", CreatedAt: now, UpdatedAt: now}
	portfolio := decisions.DecisionRecord{ID: kernel.NewID(), Title: "Портфельное", Status: decisions.StatusAccepted, Author: "cpo",
		CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)}
	for _, r := range []decisions.DecisionRecord{rec, portfolio} {
		if err := store.Save(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	rec.Status, rec.PageID, rec.SupersededBy = decisions.StatusSuperseded, "page-1", portfolio.ID
	if err := store.Save(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Get(ctx, rec.ID); err != nil || !reflect.DeepEqual(got, rec) {
		t.Fatalf("Get:\n got %+v\nwant %+v\nerr=%v", got, rec, err)
	}
	got, err := store.Get(ctx, portfolio.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Options == nil {
		got.Options = nil
	}
	portfolio.Options = []decisions.Option{}
	if !reflect.DeepEqual(got, portfolio) {
		t.Fatalf("Get portfolio:\n got %+v\nwant %+v", got, portfolio)
	}
	cases := map[string]struct {
		f    decisions.Filter
		want []kernel.ID
	}{
		"all":         {decisions.Filter{}, []kernel.ID{rec.ID, portfolio.ID}},
		"product":     {decisions.Filter{ProductID: product, HasProduct: true}, []kernel.ID{rec.ID}},
		"portfolio":   {decisions.Filter{HasProduct: true}, []kernel.ID{portfolio.ID}},
		"status":      {decisions.Filter{Status: decisions.StatusAccepted}, []kernel.ID{portfolio.ID}},
		"link hyp":    {decisions.Filter{Link: &decisions.Link{Kind: decisions.LinkHypothesis, ID: hyp}}, []kernel.ID{rec.ID}},
		"link other":  {decisions.Filter{Link: &decisions.Link{Kind: decisions.LinkSignal, ID: hyp}}, nil},
		"other prd":   {decisions.Filter{ProductID: kernel.NewID(), HasProduct: true}, nil},
		"status+link": {decisions.Filter{Status: decisions.StatusSuperseded, Link: &decisions.Link{Kind: decisions.LinkFeature, ID: feature}}, []kernel.ID{rec.ID}},
	}
	for name, c := range cases {
		list, err := store.List(ctx, c.f)
		if err != nil {
			t.Fatalf("List %s: %v", name, err)
		}
		ids := make([]kernel.ID, 0, len(list))
		for _, r := range list {
			ids = append(ids, r.ID)
		}
		if len(ids) == 0 {
			ids = nil
		}
		if !reflect.DeepEqual(ids, c.want) {
			t.Fatalf("List %s: got %v want %v", name, ids, c.want)
		}
	}
	if _, err := store.Get(ctx, kernel.NewID()); !kernel.IsNotFound(err) {
		t.Fatalf("Get missing: %v", err)
	}
	event := kernel.NewID()
	if ok, err := store.EventProcessed(ctx, event); err != nil || ok {
		t.Fatalf("EventProcessed before: %v err=%v", ok, err)
	}
	if err := store.MarkEventProcessed(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkEventProcessed(ctx, event); err != nil {
		t.Fatalf("повторная отметка должна быть идемпотентной: %v", err)
	}
	if ok, err := store.EventProcessed(ctx, event); err != nil || !ok {
		t.Fatalf("EventProcessed after: %v err=%v", ok, err)
	}
}
