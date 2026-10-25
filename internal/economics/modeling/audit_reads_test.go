package economics_test

import (
	"context"
	"errors"
	"testing"

	economics "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/economics/modeling/formula"
	"github.com/onixus/metis/internal/kernel"
	"github.com/shopspring/decimal"
)

type modelReadAuditor struct {
	err   error
	calls int
}

func (a *modelReadAuditor) FinanceAccess(context.Context, string, string, string, kernel.ID, map[string]any) error {
	a.calls++
	return a.err
}

func TestAD04_NFS02_ModelFinancialReadsFailWhenAuditUnavailable(t *testing.T) {
	f := newFixture(t)
	f.loadControlData()
	m, err := f.svc.SaveMetric(f.ctx, f.fin, economics.MetricInput{Key: "audit_metric", Name: "Synthetic", Expression: `field("revenue")`})
	if err != nil {
		t.Fatal(err)
	}
	sn, err := f.svc.SaveScenario(f.ctx, f.fin, economics.Scenario{Name: "Synthetic", Period: jan(), Products: []kernel.ID{f.edr}})
	if err != nil {
		t.Fatal(err)
	}
	sl := economics.Slice{ProductID: f.edr, Period: jan()}
	failure := errors.New("synthetic audit unavailable")
	auditor := &modelReadAuditor{err: failure}
	f.svc.WithAuditor(auditor)
	checks := []struct {
		name string
		run  func() error
	}{
		{"value", func() error { _, e := f.svc.Value(f.ctx, f.fin, m.Key, sl); return e }},
		{"explain", func() error { _, e := f.svc.Explain(f.ctx, f.fin, m.Key, sl); return e }},
		{"compare", func() error { _, _, e := f.svc.CompareVersions(f.ctx, f.fin, m.Key, sl, 1, 1); return e }},
		{"facts", func() error { _, e := f.svc.Facts(f.ctx, f.fin, economics.FactFilter{Product: &f.edr}); return e }},
		{"product", func() error { _, e := f.svc.ProductPnL(f.ctx, f.fin, f.edr, jan()); return e }},
		{"portfolio", func() error { _, e := f.svc.PortfolioPnL(f.ctx, f.fin, jan()); return e }},
		{"team", func() error { _, e := f.svc.TeamCosts(f.ctx, f.fin, kernel.NewID(), jan()); return e }},
		{"matrix", func() error { _, e := f.svc.Matrix(f.ctx, f.fin, jan()); return e }},
		{"feature", func() error { _, e := f.svc.FeatureEconomics(f.ctx, f.fin, f.edr, kernel.NewID(), jan()); return e }},
		{"branches", func() error { _, e := f.svc.BranchCosts(f.ctx, f.fin, f.edr, jan()); return e }},
		{"certification", func() error { _, e := f.svc.CertificationEconomics(f.ctx, f.fin, f.edr, jan()); return e }},
		{"scenario", func() error { _, e := f.svc.RunScenario(f.ctx, f.fin, sn.ID, nil); return e }},
		{"history", func() error { _, e := f.svc.Batches(f.ctx, f.fin, jan()); return e }},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			before := auditor.calls
			if err := tc.run(); !errors.Is(err, failure) {
				t.Fatalf("financial read bypassed audit: %v", err)
			}
			if auditor.calls != before+1 {
				t.Fatalf("audit calls %d → %d", before, auditor.calls)
			}
		})
	}
}

func TestAD04_EC02_EC04_EC11_ModelFinancialWritesFailWhenAuditUnavailable(t *testing.T) {
	f := newFixture(t)
	if err := f.svc.ClosePeriod(f.ctx, f.fin, jan()); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("synthetic audit unavailable")
	f.svc.WithAuditor(&modelReadAuditor{err: failure})
	checks := []struct {
		name string
		run  func() error
	}{
		{"close", func() error { return f.svc.ClosePeriod(f.ctx, f.fin, jan()) }},
		{"recalculate", func() error {
			_, err := f.svc.Recalculate(f.ctx, f.fin, formula.Ref{Kind: formula.RefField, Key: "revenue"}, economics.Slice{ProductID: f.edr, Period: jan()}, true)
			return err
		}},
		{"allocation", func() error {
			_, err := f.svc.SaveAllocationRule(f.ctx, f.fin, economics.AllocationInput{HubProductID: f.hub, Basis: economics.BasisManual, EffectiveFrom: kernel.DateOf(2026, 2, 1), Shares: map[kernel.ID]decimal.Decimal{f.edr: decimal.NewFromInt(1)}})
			return err
		}},
		{"bundle", func() error {
			_, err := f.svc.SaveBundleRule(f.ctx, f.fin, economics.BundleInput{BundleKey: "synthetic", EffectiveFrom: kernel.DateOf(2026, 2, 1), Shares: map[kernel.ID]decimal.Decimal{f.edr: decimal.NewFromInt(1)}})
			return err
		}},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, failure) {
				t.Fatalf("write bypassed audit: %v", err)
			}
		})
	}
}
