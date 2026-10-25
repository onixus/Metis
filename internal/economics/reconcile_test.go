package economics_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onixus/metis/internal/economics"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
	"github.com/shopspring/decimal"
)

func reconciliationFixture(t *testing.T) (*economics.Service, authz.Scope, economics.Snapshot, ports.FinancePreview) {
	t.Helper()
	svc, _, _, sc, in, target := fixture(t)
	in.Rows = []economics.Row{
		{ProductID: in.ProductID, Category: economics.Revenue, Amount: kernel.RUB(100), Description: "licenses", Source: economics.Source{File: "finance.csv", Row: 2, Hash: "old"}},
		{ProductID: in.ProductID, Category: economics.DirectCost, Amount: kernel.RUB(30), Description: "platform", Source: economics.Source{File: "finance.csv", Row: 3, Hash: "old"}, Values: map[string]string{"hours": "10"}, Allocations: []economics.Allocation{{ProductID: target, Share: decimal.NewFromInt(1)}}, AllocationSource: "manual"},
	}
	in.Fields = []economics.Field{{Key: "hours", Type: "number", Source: "manual"}}
	snap, err := svc.Save(context.Background(), sc, in)
	if err != nil {
		t.Fatal(err)
	}
	// Reordered rows with corrected amounts and new lineage must retain their business identity.
	preview := ports.FinancePreview{SourceHash: "new", Rows: []ports.FinanceRow{
		{ProductID: in.ProductID, Period: in.Period, Category: economics.DirectCost, Amount: kernel.RUB(40), Description: "platform", Source: ports.FinanceSource{File: "finance.csv", Row: 2, Hash: "new"}},
		{ProductID: in.ProductID, Period: in.Period, Category: economics.Revenue, Amount: kernel.RUB(150), Description: "licenses", Source: ports.FinanceSource{File: "finance.csv", Row: 3, Hash: "new"}},
	}}
	return svc, sc, snap, preview
}

func TestEC07_EC11_EC12_ReconciliationPreservesRulesAcrossReorderAndCorrection(t *testing.T) {
	svc, sc, old, preview := reconciliationFixture(t)
	ctx := context.Background()
	result, err := svc.PreviewReconciliation(ctx, sc, old.ProductID, old.Period, preview, 1)
	if err != nil {
		t.Fatal(err)
	}
	if result.Matched != 2 || result.Changed != 2 || result.Added != 0 || result.Removed != 0 {
		t.Fatalf("wrong reconciliation: %+v", result)
	}
	if len(result.Totals) != 2 || result.Totals[0].Previous != "30" || result.Totals[0].Current != "40" || result.Totals[0].Delta != "10" {
		t.Fatalf("totals: %+v", result.Totals)
	}
	history, err := svc.History(ctx, sc, old.ProductID, old.Period)
	if err != nil || len(history) != 1 {
		t.Fatalf("preview changed history: %v %v", history, err)
	}
	current, err := svc.ImportReconciled(ctx, sc, old.ProductID, old.Period, preview, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	cost := current.Rows[0]
	if cost.ID != old.Rows[1].ID || cost.Amount.Amount != 40 || cost.Source.Hash != "new" || cost.Source.Row != 2 || cost.Values["hours"] != "10" || len(cost.Allocations) != 1 || cost.AllocationSource != "manual" {
		t.Fatalf("lost rules or source data: %+v", cost)
	}
	historic, err := svc.Snapshot(ctx, sc, old.ProductID, old.Period, 1)
	if err != nil || historic.Rows[1].Amount.Amount != 30 || historic.Rows[1].Source.Hash != "old" {
		t.Fatalf("history mutated: %+v %v", historic, err)
	}
}

func TestEC07_ReconciliationRejectsAmbiguityAndConfiguredRemoval(t *testing.T) {
	for _, mode := range []string{"duplicate", "removed", "renamed", "source-renamed"} {
		t.Run(mode, func(t *testing.T) {
			svc, sc, old, p := reconciliationFixture(t)
			switch mode {
			case "duplicate":
				p.Rows = append(p.Rows, p.Rows[0])
			case "removed":
				p.Rows = p.Rows[1:]
			case "renamed":
				p.Rows[0].Description = "different-cost"
			case "source-renamed":
				p.Rows[0].Source.File = "other.csv"
			}
			if _, err := svc.PreviewReconciliation(context.Background(), sc, old.ProductID, old.Period, p, 1); !errors.Is(err, kernel.ErrValidation) {
				t.Fatalf("unsafe preview: %v", err)
			}
			if _, err := svc.ImportReconciled(context.Background(), sc, old.ProductID, old.Period, p, 1, false); !errors.Is(err, kernel.ErrValidation) {
				t.Fatalf("unsafe import: %v", err)
			}
			history, err := svc.History(context.Background(), sc, old.ProductID, old.Period)
			if err != nil || len(history) != 1 {
				t.Fatalf("failed import mutated history: %+v %v", history, err)
			}
		})
	}
}

func TestEC07_EC11_ReconciliationPreservesOptimisticLockAndClosedPeriod(t *testing.T) {
	svc, sc, old, p := reconciliationFixture(t)
	ctx := context.Background()
	if _, err := svc.PreviewReconciliation(ctx, sc, old.ProductID, old.Period, p, 0); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("stale preview: %v", err)
	}
	if _, err := svc.ImportReconciled(ctx, sc, old.ProductID, old.Period, p, 0, false); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("stale import: %v", err)
	}
	if _, err := svc.Close(ctx, sc, old.ProductID, old.Period, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ImportReconciled(ctx, sc, old.ProductID, old.Period, p, 2, false); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("implicit reopen: %v", err)
	}
	result, err := svc.ImportReconciled(ctx, sc, old.ProductID, old.Period, p, 2, true)
	if err != nil || !result.Closed || result.Version != 3 {
		t.Fatalf("explicit recalc: %+v %v", result, err)
	}
}

func TestEC07_NFS01_ReconciliationRejectsUnauthorizedOldAndNewProducts(t *testing.T) {
	svc, _, old, p := reconciliationFixture(t)
	ctx := context.Background()
	for _, sc := range []authz.Scope{{}, authz.New(authz.Params{Subject: "restricted", Roles: []authz.Role{authz.RoleFinance}, Finance: authz.FinanceFull, Products: map[kernel.ID]authz.Access{old.ProductID: authz.AccessPrivate}})} {
		if _, err := svc.PreviewReconciliation(ctx, sc, old.ProductID, old.Period, p, 1); !errors.Is(err, kernel.ErrForbidden) {
			t.Fatalf("unauthorized preview: %v", err)
		}
		if _, err := svc.ImportReconciled(ctx, sc, old.ProductID, old.Period, p, 1, false); !errors.Is(err, kernel.ErrForbidden) {
			t.Fatalf("unauthorized import: %v", err)
		}
	}
}

func TestEC07_ReconciliationAddsAndRemovesUnconfiguredFacts(t *testing.T) {
	svc, sc, old, p := reconciliationFixture(t)
	// Keep the configured cost and replace the unconfigured revenue row.
	p.Rows[1].Description = "new-channel"
	p.Rows[1].Amount = kernel.RUB(170)
	result, err := svc.PreviewReconciliation(context.Background(), sc, old.ProductID, old.Period, p, 1)
	if err != nil {
		t.Fatal(err)
	}
	if result.Matched != 1 || result.Changed != 1 || result.Added != 1 || result.Removed != 1 {
		t.Fatalf("reconciliation counts: %+v", result)
	}
	next, err := svc.ImportReconciled(context.Background(), sc, old.ProductID, old.Period, p, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if next.Rows[0].ID != old.Rows[1].ID || next.Rows[1].ID == old.Rows[0].ID {
		t.Fatalf("incorrect identity transfer: %+v", next.Rows)
	}
}

func TestEC07_OrdinaryImportStillExplicitlyResetsRules(t *testing.T) {
	svc, sc, old, p := reconciliationFixture(t)
	next, err := svc.Import(context.Background(), sc, old.ProductID, old.Period, p, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range next.Rows {
		if len(row.Values) != 0 || len(row.Allocations) != 0 || row.AllocationSource != "" {
			t.Fatalf("ordinary import retained rules: %+v", row)
		}
	}
	if len(next.Fields) != 1 {
		t.Fatalf("field definitions lost: %+v", next.Fields)
	}
}

func TestEC07_ReconciliationRejectsPreviouslyAmbiguousKeys(t *testing.T) {
	svc, sc, old, p := reconciliationFixture(t)
	rows := append([]economics.Row{}, old.Rows...)
	duplicate := old.Rows[0]
	duplicate.ID = kernel.NewID()
	rows = append(rows, duplicate)
	_, err := svc.Save(context.Background(), sc, economics.SaveInput{ProductID: old.ProductID, Period: old.Period, Currency: old.Currency, ExpectedVersion: 1, Rows: rows, Fields: old.Fields})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ImportReconciled(context.Background(), sc, old.ProductID, old.Period, p, 2, false); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("ambiguous old keys accepted: %v", err)
	}
}

func TestEC07_EC08_ReconciliationCarriesCanonicalManualItems(t *testing.T) {
	svc, sc, snapshot, preview := reconciliationFixture(t)
	ctx := context.Background()
	rows := append([]economics.Row{}, snapshot.Rows...)
	for i, item := range []string{"first", "second"} {
		rows = append(rows, economics.Row{ProductID: snapshot.ProductID, Category: economics.DirectCost, Amount: kernel.RUB(0), ModelItem: item, Source: economics.Source{File: economics.CanonicalManualSource, Sheet: "hours", Row: i + 1, Hash: "synthetic-manual"}, Values: map[string]string{"hours": []string{"1", "2"}[i]}, Allocations: []economics.Allocation{}})
	}
	current, err := svc.Save(ctx, sc, economics.SaveInput{ProductID: snapshot.ProductID, Period: snapshot.Period, Currency: snapshot.Currency, ExpectedVersion: snapshot.Version, Rows: rows, Fields: snapshot.Fields})
	if err != nil {
		t.Fatal(err)
	}
	next, err := svc.ImportReconciled(ctx, sc, current.ProductID, current.Period, preview, current.Version, false)
	if err != nil {
		t.Fatal(err)
	}
	items := map[string]string{}
	for _, r := range next.Rows {
		if r.Source.File == economics.CanonicalManualSource {
			items[r.ModelItem] = r.Values["hours"]
		}
	}
	if len(next.Rows) != 4 || items["first"] != "1" || items["second"] != "2" {
		t.Fatalf("canonical manual input lost/recombined: %+v", next.Rows)
	}
}
