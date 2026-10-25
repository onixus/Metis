package app

import (
	"context"
	"errors"
	"testing"

	"github.com/onixus/metis/internal/economics"
	modeling "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

type commonFormulaMemTx struct{ *modeling.MemStore }

func (m commonFormulaMemTx) Transact(ctx context.Context, _ authz.Scope, fn func(context.Context) error) error {
	return fn(ctx)
}

func TestEC08_EC09_EC11_EC13_CurrentCommonFormulaOwnsBookUpdates(t *testing.T) {
	a, _, original, _ := worklogApp(t)
	ctx, sc := context.Background(), identityaccess.FinanceServiceScope("synthetic-current-formulas")
	svc, err := modeling.NewService(commonFormulaMemTx{modeling.NewMemStore()}, nil, kernel.SystemClock{}, modeling.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	a.Modeling = svc.WithAuditor(financeAuditor{a.Audit})
	a.configureUnifiedEconomics()
	rows := []economics.Row{{ProductID: original.ProductID, Category: economics.Revenue, Amount: kernel.RUB(100)}, {ProductID: original.ProductID, Category: economics.Payroll, Amount: kernel.RUB(100), TeamID: "synthetic", Headcount: 1}}
	snapshot, err := a.Economics.Save(ctx, sc, economics.SaveInput{ProductID: original.ProductID, Period: original.Period, Currency: original.Currency, ExpectedVersion: original.Version, Rows: rows, Fields: []economics.Field{{Key: "ratio", Name: "Original ratio", Source: "calculated", Type: "number", Formula: "revenue/payroll"}}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := modeling.ParsePeriod(snapshot.Period)
	if err != nil {
		t.Fatal(err)
	}
	in := modeling.HistoryUnificationInput{BookProductID: snapshot.ProductID, Period: p, ExpectedBookVersion: snapshot.Version}
	preview, err := a.Modeling.PreviewUnification(ctx, sc, in)
	if err != nil {
		t.Fatal(err)
	}
	in.Digest = preview.Digest
	if _, err := a.Modeling.ApplyUnification(ctx, sc, in); err != nil {
		t.Fatal(err)
	}
	key := preview.DefinitionMappings["ratio"]
	if _, err := a.Modeling.SaveMetric(ctx, sc, modeling.MetricInput{Key: key, Name: "Safe common ratio", Expression: `d("7")`, EffectiveFrom: p.Start()}); err != nil {
		t.Fatal(err)
	}
	rows[1].Amount.Amount = 0
	previewInput := ports.FinancePreview{Rows: []ports.FinanceRow{{ProductID: snapshot.ProductID, Period: snapshot.Period, Category: economics.Revenue, Amount: kernel.RUB(100)}, {ProductID: snapshot.ProductID, Period: snapshot.Period, Category: economics.Payroll, Amount: kernel.RUB(0), TeamID: "synthetic", Headcount: 1}}}
	if _, err := a.Economics.PreviewReconciliation(ctx, sc, snapshot.ProductID, snapshot.Period, previewInput, snapshot.Version); err != nil {
		t.Fatalf("reconciliation preview ran archived DSL: %v", err)
	}
	current, err := a.Economics.Save(ctx, sc, economics.SaveInput{ProductID: snapshot.ProductID, Period: snapshot.Period, Currency: snapshot.Currency, ExpectedVersion: snapshot.Version, Rows: rows, Fields: snapshot.Fields})
	if err != nil {
		t.Fatalf("archived formula blocked valid current book: %v", err)
	}
	report, err := a.Economics.Report(ctx, sc, economics.ReportInput{ProductID: current.ProductID, Period: current.Period})
	if err != nil || report.Total.Metrics["ratio"] != "7" {
		t.Fatalf("current report: %+v %v", report, err)
	}
	scenario, err := a.Economics.Report(ctx, sc, economics.ReportInput{ProductID: current.ProductID, Period: current.Period, Version: current.Version, Overrides: map[string]string{"payroll": "0"}})
	if err != nil || scenario.Total.Metrics["ratio"] != "7" {
		t.Fatalf("current scenario: %+v %v", scenario, err)
	}
	if _, err := a.Economics.Project(ctx, sc, current.ProductID, current.Period, current.Version); err != nil {
		t.Fatalf("current projection executes archived formula: %v", err)
	}
	historical, err := a.Economics.Report(ctx, sc, economics.ReportInput{ProductID: snapshot.ProductID, Period: snapshot.Period, Version: snapshot.Version})
	if err != nil || historical.Total.Metrics["ratio"] != "1" {
		t.Fatalf("original pinned DSL changed: %+v %v", historical, err)
	}
	postBinding := current
	rows[1].Amount.Amount = 2
	current, err = a.Economics.Save(ctx, sc, economics.SaveInput{ProductID: current.ProductID, Period: current.Period, Currency: current.Currency, ExpectedVersion: current.Version, Rows: rows, Fields: current.Fields})
	if err != nil {
		t.Fatal(err)
	}
	postReport, err := a.Economics.Report(ctx, sc, economics.ReportInput{ProductID: current.ProductID, Period: current.Period, Version: postBinding.Version})
	if err != nil || postReport.Total.Metrics["ratio"] != "7" || postReport.Total.Lineage["ratio"].Formula != `d("7")` {
		t.Fatalf("pinned post-binding inputs executed archived DSL: %+v %v", postReport, err)
	}
	if _, err := a.Economics.Project(ctx, sc, current.ProductID, current.Period, postBinding.Version); err != nil {
		t.Fatalf("post-binding pinned projection: %v", err)
	}
	if _, err := a.Modeling.SaveMetric(ctx, sc, modeling.MetricInput{Key: key, Name: "Candidate denominator", Expression: `d("1")/(field("payroll")-d("1"))`, EffectiveFrom: p.Start()}); err != nil {
		t.Fatal(err)
	}
	rows[1].Amount.Amount = 1
	_, err = a.Economics.Save(ctx, sc, economics.SaveInput{ProductID: current.ProductID, Period: current.Period, Currency: current.Currency, ExpectedVersion: current.Version, Rows: rows, Fields: current.Fields})
	if !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("invalid current common formula accepted candidate: %v", err)
	}
	latest, err := a.Economics.Snapshot(ctx, sc, current.ProductID, current.Period, 0)
	if err != nil || latest.Version != current.Version || latest.Rows[1].Amount.Amount != 2 {
		t.Fatalf("failed common validation changed facts: %+v %v", latest, err)
	}
	denied := authz.New(authz.Params{Subject: "aggregate", Roles: []authz.Role{authz.RoleFinance}, AllProducts: authz.AccessPrivate, Finance: authz.FinanceAggregates})
	if _, err := a.Economics.Report(ctx, denied, economics.ReportInput{ProductID: current.ProductID, Period: current.Period}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("inputs-only bypassed full finance: %v", err)
	}
}
