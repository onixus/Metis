package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/economics"
	modeling "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

func TestEC03_EC09_EC10_EC11_BookMetricsUsePinnedFactsAndSharedEngine(t *testing.T) {
	a, _, snapshot, _ := worklogApp(t)
	ctx, sc := context.Background(), identityaccess.FinanceServiceScope("synthetic-book-model")
	effective := kernel.DateOf(2026, time.January, 1)
	for _, key := range []string{"revenue", "payroll", "noise"} {
		_, err := a.Modeling.SaveField(ctx, sc, modeling.FieldInput{Key: key, Name: key, Type: modeling.FieldMoney, Source: modeling.SourceManual, Currency: "RUB", EffectiveFrom: effective})
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := a.Modeling.SaveMetric(ctx, sc, modeling.MetricInput{Key: "book_profit", Name: "Book profit", Expression: `field("revenue") - field("payroll") + field("noise")`, Currency: "RUB", EffectiveFrom: effective})
	if err != nil {
		t.Fatal(err)
	}
	p, err := modeling.ParsePeriod(snapshot.Period)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Modeling.SetManualValue(ctx, sc, "noise", modeling.Slice{ProductID: snapshot.ProductID, Period: p}, decimal.NewFromInt(999)); err != nil {
		t.Fatal(err)
	}
	// Read-through must not pick up unrelated facts from the modeling store.
	info, metric, err := a.EvaluateBookMetric(ctx, sc, snapshot.ProductID, snapshot.Period, 1, "book_profit", kernel.NilID, "")
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != snapshot.ID || info.Version != 1 || !metric.Value.Equal(decimal.NewFromInt(899)) {
		t.Fatalf("wrong book calculation: %+v %+v", info, metric)
	}
	if len(metric.Inputs) != 3 {
		t.Fatalf("missing explanation: %+v", metric)
	}
	_, product, err := a.EvaluateBookMetric(ctx, sc, snapshot.ProductID, snapshot.Period, 1, "book_profit", snapshot.ProductID, "")
	if err != nil || !product.Value.Equal(decimal.NewFromInt(-101)) {
		t.Fatalf("product slice: %+v %v", product, err)
	}
	_, team, err := a.EvaluateBookMetric(ctx, sc, snapshot.ProductID, snapshot.Period, 1, "book_profit", kernel.NilID, "team-platform")
	if err != nil || !team.Value.Equal(decimal.NewFromInt(-101)) {
		t.Fatalf("team slice: %+v %v", team, err)
	}
	next, err := a.Economics.Save(ctx, sc, economics.SaveInput{ProductID: snapshot.ProductID, Period: snapshot.Period, Currency: snapshot.Currency, ExpectedVersion: 1, Rows: []economics.Row{{ProductID: snapshot.ProductID, Category: economics.Revenue, Amount: kernel.RUB(2000)}}})
	if err != nil {
		t.Fatal(err)
	}
	_, pinned, err := a.EvaluateBookMetric(ctx, sc, snapshot.ProductID, snapshot.Period, 1, "book_profit", kernel.NilID, "")
	if err != nil || !pinned.Value.Equal(decimal.NewFromInt(899)) {
		t.Fatalf("pinned version drifted: %+v %v", pinned, err)
	}
	info, current, err := a.EvaluateBookMetric(ctx, sc, snapshot.ProductID, snapshot.Period, 0, "book_profit", kernel.NilID, "")
	if err != nil || info.ID != next.ID || !current.Value.Equal(decimal.NewFromInt(2000)) {
		t.Fatalf("latest version: %+v %+v %v", info, current, err)
	}
	standalone, err := a.Modeling.Value(ctx, sc, "book_profit", modeling.Slice{ProductID: snapshot.ProductID, Period: p})
	if err != nil || !standalone.Equal(decimal.NewFromInt(999)) {
		t.Fatalf("model facts modified: %v %v", standalone, err)
	}
	for _, denied := range []authz.Scope{{}, authz.New(authz.Params{Subject: "aggregate", Roles: []authz.Role{authz.RoleFinance}, AllProducts: authz.AccessPrivate, Finance: authz.FinanceAggregates})} {
		if _, _, err := a.EvaluateBookMetric(ctx, denied, snapshot.ProductID, snapshot.Period, 1, "book_profit", kernel.NilID, ""); !errors.Is(err, kernel.ErrForbidden) {
			t.Fatalf("unauthorized book facts: %v", err)
		}
	}
}

func TestEC08_EC09_BookNumberCannotBecomeModelMoney(t *testing.T) {
	a, _, snapshot, _ := worklogApp(t)
	ctx, sc := context.Background(), identityaccess.FinanceServiceScope("synthetic-types")
	rows := kernel.CloneValue(snapshot.Rows)
	rows[0].Values = map[string]string{"typed_input": "100"}
	next, err := a.Economics.Save(ctx, sc, economics.SaveInput{ProductID: snapshot.ProductID, Period: snapshot.Period, Currency: snapshot.Currency, ExpectedVersion: snapshot.Version, Rows: rows, Fields: []economics.Field{{Key: "typed_input", Name: "Synthetic", Type: "number", Source: "manual"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Modeling.SaveField(ctx, sc, modeling.FieldInput{Key: "typed_input", Name: "Synthetic", Type: modeling.FieldMoney, Source: modeling.SourceManual, Currency: "RUB"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Modeling.SaveMetric(ctx, sc, modeling.MetricInput{Key: "typed_book", Name: "Synthetic", Expression: `field("typed_input")`}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.EvaluateBookMetric(ctx, sc, next.ProductID, next.Period, next.Version, "typed_book", kernel.NilID, ""); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("book number silently interpreted as money: %v", err)
	}
}
