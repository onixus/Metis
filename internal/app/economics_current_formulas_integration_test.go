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
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/portfoliograph"
)

func TestEC09_EC11_EC13_NFS05_PostgresCurrentFormulasValidateCandidateAndPinnedInputs(t *testing.T) {
	isolateSourceRuntimeDatabase(t)
	a, _, _ := runtimeApps(t)
	ctx, sc := context.Background(), identityaccess.FinanceServiceScope("synthetic-current-formula")
	period := modeling.PeriodOf(2495, time.January)
	product, err := a.Portfolio.CreateProduct(ctx, identityaccess.ServiceScope("synthetic-formula-setup"), portfoliograph.ProductInput{Key: "formula-" + strings.ReplaceAll(kernel.NewID().String(), "-", ""), Name: "Synthetic formula", Type: portfoliograph.ProductTypeSecurity})
	if err != nil {
		t.Fatal(err)
	}
	var original economics.Snapshot
	var preview modeling.HistoryUnificationPreview
	rows := []economics.Row{{ProductID: product.ID, Category: economics.Revenue, Amount: kernel.RUB(100)}, {ProductID: product.ID, Category: economics.Payroll, Amount: kernel.RUB(100), TeamID: "synthetic", Headcount: 1}}
	fields := []economics.Field{{Key: "ratio", Name: "Original ratio", Type: "number", Source: "calculated", Formula: "revenue/payroll"}}
	err = a.runOperation(ctx, func(ctx context.Context) error {
		var err error
		original, err = a.Economics.Save(ctx, sc, economics.SaveInput{ProductID: product.ID, Period: period.String(), Currency: "RUB", Rows: rows, Fields: fields})
		if err != nil {
			return err
		}
		in := modeling.HistoryUnificationInput{BookProductID: product.ID, Period: period, ExpectedBookVersion: original.Version}
		preview, err = a.Modeling.PreviewUnification(ctx, sc, in)
		if err != nil {
			return err
		}
		in.Digest = preview.Digest
		if _, err = a.Modeling.ApplyUnification(ctx, sc, in); err != nil {
			return err
		}
		_, err = a.Modeling.SaveMetric(ctx, sc, modeling.MetricInput{Key: preview.DefinitionMappings["ratio"], Name: "Safe common", Expression: `d("7")`, EffectiveFrom: period.Start()})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var post, current economics.Snapshot
	for _, payroll := range []int64{0, 2} {
		rows[1].Amount.Amount = payroll
		err = a.runOperation(ctx, func(ctx context.Context) error {
			var err error
			current, err = a.Economics.Save(ctx, sc, economics.SaveInput{ProductID: product.ID, Period: period.String(), Currency: "RUB", ExpectedVersion: original.Version, Rows: rows, Fields: fields})
			return err
		})
		if err != nil {
			t.Fatalf("post-binding payroll %d: %v", payroll, err)
		}
		if payroll == 0 {
			post = current
		}
		original = current
	}
	err = a.runOperation(ctx, func(ctx context.Context) error {
		report, err := a.Economics.Report(ctx, sc, economics.ReportInput{ProductID: product.ID, Period: period.String(), Version: post.Version})
		if err != nil {
			return err
		}
		if report.Total.Metrics["ratio"] != "7" || report.Total.Lineage["ratio"].Formula != `d("7")` {
			t.Fatalf("postbinding pinned report: %+v", report)
		}
		_, err = a.Modeling.SaveMetric(ctx, sc, modeling.MetricInput{Key: preview.DefinitionMappings["ratio"], Name: "Candidate zero denominator", Expression: `d("1")/(field("payroll")-d("1"))`, EffectiveFrom: period.Start()})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var before, after int
	if err := a.db.Pool().QueryRow(ctx, "SELECT count(*) FROM audit.records").Scan(&before); err != nil {
		t.Fatal(err)
	}
	rows[1].Amount.Amount = 1
	err = a.runOperation(ctx, func(ctx context.Context) error {
		_, err := a.Economics.Save(ctx, sc, economics.SaveInput{ProductID: product.ID, Period: period.String(), Currency: "RUB", ExpectedVersion: current.Version, Rows: rows, Fields: fields})
		return err
	})
	if !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("candidate formula failure: %v", err)
	}
	if err := a.db.Pool().QueryRow(ctx, "SELECT count(*) FROM audit.records").Scan(&after); err != nil || before != after {
		t.Fatalf("failed candidate leaked audit %d→%d: %v", before, after, err)
	}
	latest, err := a.Economics.Snapshot(ctx, sc, product.ID, period.String(), 0)
	if err != nil || latest.Version != current.Version || latest.Rows[1].Amount.Amount != 2 {
		t.Fatalf("failed candidate appended financial version: %+v %v", latest, err)
	}
}
