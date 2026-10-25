//go:build integration

package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onixus/metis/internal/economics"
	modeling "github.com/onixus/metis/internal/economics/modeling"
	bookpg "github.com/onixus/metis/internal/economics/pgstore"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/portfoliograph"
	"github.com/shopspring/decimal"
)

type failCanonicalAppend struct{ economics.Store }

func (s failCanonicalAppend) Append(ctx context.Context, sc authz.Scope, snapshot economics.Snapshot, expected int) error {
	if err := s.Store.Append(ctx, sc, snapshot, expected); err != nil {
		return err
	}
	return kernel.ErrUnavailable
}

func TestEC08_EC09_EC11_NFS05_PostgresCanonicalManualUsesBookAndRollsBack(t *testing.T) {
	isolateSourceRuntimeDatabase(t)
	a, _, _ := runtimeApps(t)
	ctx := context.Background()
	sc := identityaccess.FinanceServiceScope("synthetic-manual")
	setup := identityaccess.ServiceScope("synthetic-setup")
	period := modeling.PeriodOf(2497, time.January)
	product, err := a.Portfolio.CreateProduct(ctx, setup, portfoliograph.ProductInput{Key: "manual-" + strings.ReplaceAll(kernel.NewID().String(), "-", ""), Name: "Synthetic manual", Type: portfoliograph.ProductTypeSecurity})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot economics.Snapshot
	if err := a.runOperation(ctx, func(ctx context.Context) error {
		var err error
		snapshot, err = a.Economics.Save(ctx, sc, economics.SaveInput{ProductID: product.ID, Period: period.String(), Currency: "RUB", Rows: []economics.Row{{ProductID: product.ID, Category: economics.Revenue, Amount: kernel.RUB(200)}}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	in := modeling.HistoryUnificationInput{BookProductID: product.ID, Period: period, ExpectedBookVersion: 1}
	if err := a.runOperation(ctx, func(ctx context.Context) error {
		preview, err := a.Modeling.PreviewUnification(ctx, sc, in)
		if err != nil {
			return err
		}
		in.Digest = preview.Digest
		_, err = a.Modeling.ApplyUnification(ctx, sc, in)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"pg_manual", "pg_rollback"} {
		if _, err := a.Modeling.SaveField(ctx, sc, modeling.FieldInput{Key: key, Name: key, Type: modeling.FieldNumber, Source: modeling.SourceManual, EffectiveFrom: period.Start()}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.Modeling.SaveMetric(ctx, sc, modeling.MetricInput{Key: "pg_manual_value", Name: "Manual value", Expression: `field("pg_manual")`, EffectiveFrom: period.Start()}); err != nil {
		t.Fatal(err)
	}
	sl := modeling.Slice{ProductID: product.ID, Period: period}
	written, err := a.Modeling.SetManualValue(ctx, sc, "pg_manual", sl, decimal.New(123456, -2))
	if err != nil || written.DataVersion != 2 {
		t.Fatalf("canonical writer: %+v %v", written, err)
	}
	saved, err := a.Economics.Snapshot(ctx, sc, product.ID, period.String(), 0)
	if err != nil || saved.ID != written.BatchID || saved.Version != 2 {
		t.Fatalf("book provenance: %+v %v", saved, err)
	}
	var report economics.Report
	if err := a.runOperation(ctx, func(ctx context.Context) error {
		var err error
		report, err = a.Economics.Report(ctx, sc, economics.ReportInput{ProductID: product.ID, Period: period.String()})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if report.Total.Metrics["pg_manual"] != "1234.56" {
		t.Fatalf("book value: %+v", report.Total)
	}
	value, err := a.Modeling.Value(ctx, sc, "pg_manual_value", sl)
	if err != nil || value.String() != "1234.56" {
		t.Fatalf("model value: %v %v", value, err)
	}
	if batches, err := a.Modeling.Batches(ctx, sc, period); err != nil || len(batches) != 0 {
		t.Fatalf("legacy batch created: %+v %v", batches, err)
	}
	original := a.Economics
	failed := economics.NewService(failCanonicalAppend{bookpg.New(a.db)}, a.Audit, kernel.SystemClock{}).WithReferences(financeReferences{app: a})
	failed.WithDefinitionPolicy(unifiedEconomics{a.Modeling, failed})
	a.Economics = failed
	if _, err := a.Modeling.SetManualValue(ctx, sc, "pg_rollback", sl, decimal.NewFromInt(88)); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("book append failure ignored: %v", err)
	}
	a.Economics = original
	unchanged, err := a.Economics.Snapshot(ctx, sc, product.ID, period.String(), 0)
	if err != nil || unchanged.ID != saved.ID || len(unchanged.Fields) != len(saved.Fields) {
		t.Fatalf("partial book schema/value committed: %+v %v", unchanged, err)
	}
	if err := a.runOperation(ctx, func(ctx context.Context) error {
		_, err := a.Economics.Close(ctx, sc, product.ID, period.String(), saved.Version)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Modeling.SetManualValue(ctx, sc, "pg_manual", sl, decimal.NewFromInt(1)); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("closed canonical writer: %v", err)
	}
	_ = snapshot
}
