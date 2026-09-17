//go:build integration

package pgstore_test

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/migrate"
	"github.com/onixus/metis/internal/kernel/pgdb"
	"github.com/onixus/metis/internal/prioritization"
	"github.com/onixus/metis/internal/prioritization/pgstore"
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
	if _, err := db.Pool().Exec(ctx, "TRUNCATE prioritization.feature_inputs, prioritization.models, prioritization.feature_flags, prioritization.dev_costs"); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestPR01_PGStoreModelsAndInputs(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := pgstore.New(db)
	now := time.Date(2026, 9, 17, 10, 30, 0, 0, time.UTC)
	product, feature := kernel.NewID(), kernel.NewID()
	portfolio := prioritization.ScoringModel{ID: kernel.NewID(), Name: "RICE", Type: prioritization.ModelRICE, Formula: prioritization.FormulaRICE,
		Inputs: prioritization.InputsRICE, CreatedAt: now, UpdatedAt: now}
	custom := prioritization.ScoringModel{ID: kernel.NewID(), ProductID: product, Name: "Custom", Type: prioritization.ModelCustom, Formula: "a * arr",
		Inputs: []string{"a"}, CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)}
	for _, m := range []prioritization.ScoringModel{portfolio, custom} {
		if err := store.SaveModel(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	custom.Name = "Custom v2"
	if err := store.SaveModel(ctx, custom); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Model(ctx, portfolio.ID); err != nil || !reflect.DeepEqual(got, portfolio) {
		t.Fatalf("Model portfolio: %+v err=%v", got, err)
	}
	if got, err := store.Model(ctx, custom.ID); err != nil || !reflect.DeepEqual(got, custom) {
		t.Fatalf("Model custom: %+v err=%v", got, err)
	}
	if models, err := store.Models(ctx); err != nil || len(models) != 2 || models[0].ID != portfolio.ID {
		t.Fatalf("Models: %+v err=%v", models, err)
	}
	if _, err := store.Model(ctx, kernel.NewID()); !kernel.IsNotFound(err) {
		t.Fatalf("Model missing: %v", err)
	}
	in := prioritization.FeatureScoreInput{ModelID: custom.ID, FeatureID: feature, ProductID: product,
		Values: map[string]decimal.Decimal{"a": decimal.RequireFromString("1.25")}, UpdatedAt: now, UpdatedBy: "pm"}
	if err := store.SaveInputs(ctx, in); err != nil {
		t.Fatal(err)
	}
	in.Values["a"] = decimal.RequireFromString("2.5")
	if err := store.SaveInputs(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, err := store.Inputs(ctx, custom.ID, feature)
	if err != nil || !got.Values["a"].Equal(decimal.RequireFromString("2.5")) || got.UpdatedBy != "pm" || !got.UpdatedAt.Equal(now) {
		t.Fatalf("Inputs: %+v err=%v", got, err)
	}
	if list, err := store.InputsByProduct(ctx, custom.ID, product); err != nil || len(list) != 1 || list[0].FeatureID != feature {
		t.Fatalf("InputsByProduct: %+v err=%v", list, err)
	}
	if _, err := store.Inputs(ctx, portfolio.ID, feature); !kernel.IsNotFound(err) {
		t.Fatalf("Inputs missing: %v", err)
	}
}

func TestPR04_PGStoreFlags(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := pgstore.New(db)
	now := time.Date(2026, 9, 17, 10, 30, 0, 0, time.UTC)
	f := prioritization.FeatureFlags{FeatureID: kernel.NewID(), ProductID: kernel.NewID(), RegulatoryMandatory: true, Reason: "ФЗ", SetBy: "pm", SetAt: now}
	if _, err := store.Flags(ctx, f.FeatureID); !kernel.IsNotFound(err) {
		t.Fatalf("Flags before save: %v", err)
	}
	if err := store.SaveFlags(ctx, f); err != nil {
		t.Fatal(err)
	}
	f.RegulatoryMandatory = false
	if err := store.SaveFlags(ctx, f); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Flags(ctx, f.FeatureID); err != nil || !reflect.DeepEqual(got, f) {
		t.Fatalf("Flags: %+v err=%v", got, err)
	}
}

func TestPR05_PGStoreDevCost(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := pgstore.New(db)
	product, feature := kernel.NewID(), kernel.NewID()
	if _, err := store.DevCost(ctx, feature); !kernel.IsNotFound(err) {
		t.Fatalf("DevCost before save: %v", err)
	}
	if err := store.SaveDevCost(ctx, product, feature, kernel.RUB(500_000_00)); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDevCost(ctx, product, feature, kernel.RUB(600_000_00)); err != nil {
		t.Fatal(err)
	}
	if got, err := store.DevCost(ctx, feature); err != nil || got != kernel.RUB(600_000_00) {
		t.Fatalf("DevCost: %+v err=%v", got, err)
	}
}
