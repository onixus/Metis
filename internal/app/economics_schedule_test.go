package app

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/onixus/metis/internal/adapters/financefile"
	"github.com/onixus/metis/internal/economics"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

type staticFinanceSource struct {
	preview ports.FinancePreview
	err     error
}

func (s staticFinanceSource) Read(context.Context) (ports.FinancePreview, error) {
	return s.preview, s.err
}

func TestEC01_EC07_ScheduledCompositeFinancePreservesAllSources(t *testing.T) {
	a := runtimeApp(t, Config{}, false)
	f := runtimeFeature(t, a)
	ctx := context.Background()
	sc := identityaccess.FinanceServiceScope("test")
	parse := func(category, amount string) ports.FinancePreview {
		t.Helper()
		data := fmt.Sprintf("product_id,period,category,amount,currency,team_id,headcount\n%s,2026-09,%s,%s,RUB,team-zup,6\n", f.ProductID, category, amount)
		p, err := financefile.New().Parse(ctx, "synthetic.csv", []byte(data), ports.FinanceTemplate{})
		if err != nil || len(p.Errors) > 0 {
			t.Fatalf("parse: %v %+v", err, p.Errors)
		}
		return p
	}
	a.financeBook = f.ProductID
	a.financeSources = []ports.FinanceSourceReader{staticFinanceSource{preview: parse("revenue", "10000")}, staticFinanceSource{preview: parse("payroll", "3000")}}
	batches, err := a.collectFinance(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.applyFinance(ctx, batches); err != nil {
		t.Fatal(err)
	}
	if err := a.applyFinance(ctx, batches); err != nil {
		t.Fatal(err)
	}
	h, err := a.Economics.History(ctx, sc, f.ProductID, "2026-09")
	if err != nil || len(h) != 1 {
		t.Fatalf("unchanged schedule imported again: %v %v", h, err)
	}
	r, err := a.Economics.Report(ctx, sc, economics.ReportInput{ProductID: f.ProductID, Period: "2026-09"})
	if err != nil || r.Total.LoadedProfit != 700000 {
		t.Fatalf("composite: %+v %v", r, err)
	}
	if _, err := a.Economics.Close(ctx, sc, f.ProductID, "2026-09", 1); err != nil {
		t.Fatal(err)
	}
	a.financeSources[1] = staticFinanceSource{preview: parse("payroll", "4000")}
	batches, err = a.collectFinance(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.applyFinance(ctx, batches); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("schedule changed closed period: %v", err)
	}
	a.financeSources[1] = staticFinanceSource{err: kernel.ErrUnavailable}
	if _, err := a.collectFinance(ctx); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatal("partial collection accepted")
	}
}

func TestEC01_ScheduledSourcesMustCoverSamePeriods(t *testing.T) {
	a := runtimeApp(t, Config{}, false)
	p := ports.FinanceRow{Period: "2026-09"}
	q := ports.FinanceRow{Period: "2026-08"}
	a.financeSources = []ports.FinanceSourceReader{staticFinanceSource{preview: ports.FinancePreview{Rows: []ports.FinanceRow{p}}}, staticFinanceSource{preview: ports.FinancePreview{Rows: []ports.FinanceRow{p, q}}}}
	if _, err := a.collectFinance(context.Background()); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("partial financial month accepted: %v", err)
	}
}

func TestEC02_EC07_SchedulerDoesNotDiscardReviewedAllocation(t *testing.T) {
	a := runtimeApp(t, Config{}, false)
	f := runtimeFeature(t, a)
	ctx := context.Background()
	sc := identityaccess.FinanceServiceScope("test")
	a.financeBook = f.ProductID
	before, err := a.Economics.Save(ctx, sc, economics.SaveInput{ProductID: f.ProductID, Period: "2026-09", Currency: "RUB", SourceHash: "old", Rows: []economics.Row{{ProductID: f.ProductID, Category: economics.Payroll, Amount: kernel.Money{Amount: 10000, Currency: "RUB"}, TeamID: "team", Headcount: 6, Values: map[string]string{"budget": "100"}}}, Fields: []economics.Field{{Key: "budget", Name: "Manual budget", Type: "money", Source: "manual"}}})
	if err != nil {
		t.Fatal(err)
	}
	preview := ports.FinancePreview{SourceHash: "new", Rows: []ports.FinanceRow{{ProductID: f.ProductID, Period: "2026-09", Category: economics.Payroll, Amount: kernel.Money{Amount: 20000, Currency: "RUB"}, TeamID: "team", Headcount: 6, Source: ports.FinanceSource{Hash: "new"}}}}
	if err := a.applyFinance(ctx, map[string]ports.FinancePreview{"2026-09": preview}); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("rules silently discarded: %v", err)
	}
	after, err := a.Economics.Snapshot(ctx, sc, f.ProductID, "2026-09", 0)
	if err != nil || after.Version != before.Version || after.Rows[0].Values["budget"] != "100" {
		t.Fatalf("fact changed: %+v %v", after, err)
	}
}
