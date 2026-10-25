package economics_test

import (
	"testing"

	economics "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/shopspring/decimal"
)

func TestEC10_ExplainUsesExecutedProductAggregateFilters(t *testing.T) {
	f := newFixture(t)
	f.loadControlData()
	if _, err := f.svc.SaveMetric(f.ctx, f.fin, economics.MetricInput{Key: "all_revenue", Name: "Synthetic", Expression: `sum("revenue", "product", "*") + field("revenue")`}); err != nil {
		t.Fatal(err)
	}
	ex, err := f.svc.Explain(f.ctx, f.fin, "all_revenue", economics.Slice{ProductID: f.edr, Period: jan()})
	if err != nil {
		t.Fatal(err)
	}
	if len(ex.Inputs) != 2 || len(ex.Inputs[0].Rows) != 2 || len(ex.Inputs[1].Rows) != 1 {
		t.Fatalf("lost aggregate filters: %+v", ex)
	}
	if !ex.Value.Equal(ex.Inputs[0].Value.Add(ex.Inputs[1].Value)) {
		t.Fatalf("lineage does not reconcile: %+v", ex)
	}
	seen := map[kernel.ID]bool{}
	for _, row := range ex.Inputs[0].Rows {
		seen[row.ProductID] = true
	}
	if !seen[f.edr] || !seen[f.vm] {
		t.Fatalf("aggregate omitted a contributing product: %+v", ex.Inputs[0])
	}
	limited := authz.New(authz.Params{Subject: "synthetic-reader", Roles: []authz.Role{authz.RoleFinance}, Products: map[kernel.ID]authz.Access{f.edr: authz.AccessPrivate}, Finance: authz.FinanceFull, Audience: authz.AudienceInternal})
	ex, err = f.svc.Explain(f.ctx, limited, "all_revenue", economics.Slice{ProductID: f.edr, Period: jan()})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range ex.Inputs {
		for _, row := range input.Rows {
			if row.ProductID != f.edr {
				t.Fatal("aggregate lineage leaked a foreign product")
			}
		}
	}
}

func TestEC10_ExplainUsesExecutedTeamFilterAndConditionalBranch(t *testing.T) {
	f := newFixture(t)
	a, b := kernel.NewID(), kernel.NewID()
	for i, team := range []kernel.ID{a, b} {
		if _, err := f.svc.SetManualValue(f.ctx, f.fin, "headcount", economics.Slice{ProductID: f.edr, TeamID: team, Period: jan()}, decimal.NewFromInt(int64(5+i*2))); err != nil {
			t.Fatal(err)
		}
	}
	for _, in := range []economics.MetricInput{
		{Key: "team_total", Name: "Synthetic", Expression: `sum("headcount", "team", "*")`},
		{Key: "chosen", Name: "Synthetic", Expression: `true ? metric("team_total") : field("payroll")`},
	} {
		if _, err := f.svc.SaveMetric(f.ctx, f.fin, in); err != nil {
			t.Fatal(err)
		}
	}
	ex, err := f.svc.Explain(f.ctx, f.fin, "chosen", economics.Slice{ProductID: f.edr, TeamID: a, Period: jan()})
	if err != nil {
		t.Fatal(err)
	}
	if len(ex.Inputs) != 1 || ex.Inputs[0].Key != "team_total" || len(ex.Inputs[0].Inputs) != 1 {
		t.Fatalf("explained an unexecuted branch: %+v", ex)
	}
	input := ex.Inputs[0].Inputs[0]
	if !ex.Value.Equal(decimal.NewFromInt(12)) || !input.Value.Equal(ex.Value) || len(input.Rows) != 2 {
		t.Fatalf("lost team aggregate filter: %+v", ex)
	}
}
