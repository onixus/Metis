package economics_test

import (
	"errors"
	"testing"

	economics "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/shopspring/decimal"
)

func TestEC03_AD02_ProductPnLDoesNotLoseHubAllocation(t *testing.T) {
	f := newFixture(t)
	f.loadControlData()
	if _, err := f.svc.SaveAllocationRule(f.ctx, f.fin, economics.AllocationInput{
		HubProductID: f.hub, Basis: economics.BasisManual,
		Shares: map[kernel.ID]decimal.Decimal{f.edr: dec(t, "0.6"), f.vm: dec(t, "0.4")},
	}); err != nil {
		t.Fatal(err)
	}
	limited := authz.New(authz.Params{Subject: "pm", Roles: []authz.Role{authz.RolePM}, Products: map[kernel.ID]authz.Access{f.edr: authz.AccessPrivate}, Finance: authz.FinanceAggregates})
	full, err := f.svc.ProductPnL(f.ctx, f.fin, f.edr, jan())
	if err != nil {
		t.Fatal(err)
	}
	scoped, err := f.svc.ProductPnL(f.ctx, limited, f.edr, jan())
	if err != nil {
		t.Fatal(err)
	}
	if full.HubLoad != scoped.HubLoad || full.LoadedProfit != scoped.LoadedProfit {
		t.Fatalf("same product, same data: full hub=%v profit=%v; product-only hub=%v profit=%v", full.HubLoad, full.LoadedProfit, scoped.HubLoad, scoped.LoadedProfit)
	}
}

func TestEC03_NFS01_ProductPnLRejectsForbiddenTargetsAndKeepsFactsPrivate(t *testing.T) {
	f := newFixture(t)
	f.loadControlData()
	own := authz.New(authz.Params{Subject: "pm", Roles: []authz.Role{authz.RolePM}, Products: map[kernel.ID]authz.Access{f.edr: authz.AccessPrivate}, Finance: authz.FinanceAggregates})
	for _, tc := range []struct {
		name    string
		sc      authz.Scope
		product kernel.ID
	}{
		{"zero", authz.Scope{}, f.edr}, {"foreign", own, f.vm}, {"portfolio", own, kernel.NilID}, {"no finance", noFinanceScope(), f.edr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.svc.ProductPnL(f.ctx, tc.sc, tc.product, jan()); !errors.Is(err, kernel.ErrForbidden) {
				t.Fatalf("expected denied: %v", err)
			}
		})
	}
	if _, err := f.svc.Facts(f.ctx, own, economics.FactFilter{Product: &f.edr}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("aggregate reader obtained raw facts: %v", err)
	}
}

func TestEC02_AD02_RevenueAllocationUsesHiddenConsumers(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		f := newFixture(t)
		f.loadControlData()
		rule := economics.AllocationInput{HubProductID: f.hub, Basis: economics.BasisRevenue}
		if explicit {
			rule.Consumers = []kernel.ID{f.edr, f.vm}
		}
		if _, err := f.svc.SaveAllocationRule(f.ctx, f.fin, rule); err != nil {
			t.Fatal(err)
		}
		own := authz.New(authz.Params{Subject: "pm", Products: map[kernel.ID]authz.Access{f.edr: authz.AccessPrivate}, Finance: authz.FinanceAggregates})
		got, err := f.svc.ProductPnL(f.ctx, own, f.edr, jan())
		if err != nil {
			t.Fatal(err)
		}
		if got.HubLoad != f.rub(t, 2000000) {
			t.Fatalf("explicit=%v: hub load %v", explicit, got.HubLoad)
		}
	}
}

func TestEC04_AD02_ProductPnLIncludesSharedBundleRevenue(t *testing.T) {
	f := newFixture(t)
	f.loadControlData()
	columns := []string{"bundle_revenue"}
	tpl := f.template(columns)
	data := f.book(columns, []bookRow{{period: "2026-01", item: "synthetic-bundle", values: map[string]string{"bundle_revenue": "3000000"}}})
	if _, err := f.svc.ApplyImport(f.ctx, f.fin, economics.ImportInput{TemplateID: tpl.ID, Period: jan(), FileName: "bundle.xlsx", Data: data}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SaveBundleRule(f.ctx, f.fin, economics.BundleInput{BundleKey: "synthetic-bundle", Shares: map[kernel.ID]decimal.Decimal{f.edr: dec(t, "0.7"), f.vm: dec(t, "0.3")}}); err != nil {
		t.Fatal(err)
	}
	own := authz.New(authz.Params{Subject: "pm", Products: map[kernel.ID]authz.Access{f.edr: authz.AccessPrivate}, Finance: authz.FinanceAggregates})
	got, err := f.svc.ProductPnL(f.ctx, own, f.edr, jan())
	if err != nil {
		t.Fatal(err)
	}
	if got.BundleRevenue != f.rub(t, 2100000) {
		t.Fatalf("bundle share: %v", got.BundleRevenue)
	}
}
