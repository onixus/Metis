package economics_test

import (
	"errors"
	"reflect"
	"testing"

	economics "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/kernel"
	"github.com/shopspring/decimal"
)

func TestEC02_EC04_EC11_EC12_ClosedReadsRejectRetrospectiveDefinitionsAndShares(t *testing.T) {
	f := newFixture(t)
	f.loadControlData()
	metric := economics.MetricInput{Key: "closed_profit", Name: "Closed profit", Expression: `field("revenue") - field("payroll")`}
	if _, err := f.svc.SaveMetric(f.ctx, f.fin, metric); err != nil {
		t.Fatal(err)
	}
	allocation := economics.AllocationInput{HubProductID: f.hub, Basis: economics.BasisManual, Shares: map[kernel.ID]decimal.Decimal{f.edr: dec(t, "0.6"), f.vm: dec(t, "0.4")}}
	if _, err := f.svc.SaveAllocationRule(f.ctx, f.fin, allocation); err != nil {
		t.Fatal(err)
	}
	bundle := economics.BundleInput{BundleKey: "suite", Shares: map[kernel.ID]decimal.Decimal{f.edr: dec(t, "0.7"), f.vm: dec(t, "0.3")}}
	if _, err := f.svc.SaveBundleRule(f.ctx, f.fin, bundle); err != nil {
		t.Fatal(err)
	}
	team, err := f.svc.SaveTeam(f.ctx, f.fin, "closed_team", "Synthetic closed team")
	if err != nil {
		t.Fatal(err)
	}
	shares := map[kernel.ID]decimal.Decimal{f.edr: dec(t, "0.5"), f.vm: dec(t, "0.5")}
	if err := f.svc.SetTeamShares(f.ctx, f.fin, team.ID, jan(), shares); err != nil {
		t.Fatal(err)
	}
	sl := economics.Slice{ProductID: f.edr, Period: jan()}
	baselineValue, err := f.svc.Value(f.ctx, f.fin, "closed_profit", sl)
	if err != nil {
		t.Fatal(err)
	}
	baselinePnL, err := f.svc.ProductPnL(f.ctx, f.fin, f.edr, jan())
	if err != nil {
		t.Fatal(err)
	}
	baselineShares, err := f.svc.TeamShares(f.ctx, f.fin, jan())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.ClosePeriod(f.ctx, f.fin, jan()); err != nil {
		t.Fatal(err)
	}
	for _, date := range []kernel.Date{kernel.DateOf(2026, 1, 15), kernel.DateOf(2026, 1, 31)} {
		metric.EffectiveFrom = date
		metric.Expression = `field("revenue") * d("2")`
		allocation.EffectiveFrom = date
		allocation.Shares = map[kernel.ID]decimal.Decimal{f.edr: dec(t, "0.1"), f.vm: dec(t, "0.9")}
		bundle.EffectiveFrom = date
		bundle.Shares = map[kernel.ID]decimal.Decimal{f.edr: dec(t, "0.1"), f.vm: dec(t, "0.9")}
		field := economics.FieldInput{Key: "revenue", Name: "Changed field", Type: economics.FieldMoney, Source: economics.SourceImport, Currency: "USD", EffectiveFrom: date}
		_, fieldErr := f.svc.SaveField(f.ctx, f.fin, field)
		_, metricErr := f.svc.SaveMetric(f.ctx, f.fin, metric)
		_, allocationErr := f.svc.SaveAllocationRule(f.ctx, f.fin, allocation)
		_, bundleErr := f.svc.SaveBundleRule(f.ctx, f.fin, bundle)
		for name, err := range map[string]error{"field": fieldErr, "metric": metricErr, "allocation": allocationErr, "bundle": bundleErr} {
			if !errors.Is(err, kernel.ErrConflict) {
				t.Fatalf("%s retrospective %s: %v", name, date, err)
			}
		}
	}
	newShares := map[kernel.ID]decimal.Decimal{f.edr: dec(t, "1")}
	for _, err := range []error{f.svc.SetTeamShares(f.ctx, f.fin, team.ID, jan(), newShares), f.svc.SetWorklogShares(f.ctx, f.fin, team.ID, jan(), newShares)} {
		if !errors.Is(err, kernel.ErrConflict) {
			t.Fatalf("closed team shares changed: %v", err)
		}
	}
	// Future versions are valid and cannot drift January's results.
	future := kernel.DateOf(2026, 2, 1)
	metric.EffectiveFrom = future
	allocation.EffectiveFrom = future
	bundle.EffectiveFrom = future
	if _, err := f.svc.SaveMetric(f.ctx, f.fin, metric); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SaveAllocationRule(f.ctx, f.fin, allocation); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SaveBundleRule(f.ctx, f.fin, bundle); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SaveField(f.ctx, f.fin, economics.FieldInput{Key: "revenue", Name: "Future field", Type: economics.FieldMoney, Source: economics.SourceImport, Currency: "RUB", EffectiveFrom: future}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetTeamShares(f.ctx, f.fin, team.ID, economics.PeriodOf(2026, 2), newShares); err != nil {
		t.Fatal(err)
	}
	value, err := f.svc.Value(f.ctx, f.fin, "closed_profit", sl)
	if err != nil || !value.Equal(baselineValue) {
		t.Fatalf("closed Value drifted: %s -> %s %v", baselineValue, value, err)
	}
	pnl, err := f.svc.ProductPnL(f.ctx, f.fin, f.edr, jan())
	if err != nil || !reflect.DeepEqual(pnl, baselinePnL) {
		t.Fatalf("closed PnL drifted: %+v -> %+v %v", baselinePnL, pnl, err)
	}
	actualShares, err := f.svc.TeamShares(f.ctx, f.fin, jan())
	if err != nil || !reflect.DeepEqual(actualShares, baselineShares) {
		t.Fatalf("closed shares drifted: %+v %v", actualShares, err)
	}
}

func TestEC11_ClosedPeriodRejectsFirstUndatedDefinitionsAndRules(t *testing.T) {
	f := newFixture(t)
	if err := f.svc.ClosePeriod(f.ctx, f.fin, jan()); err != nil {
		t.Fatal(err)
	}
	field := economics.FieldInput{Key: "new_field", Name: "New", Type: economics.FieldNumber, Source: economics.SourceManual}
	metric := economics.MetricInput{Key: "new_metric", Name: "New", Expression: `d("1")`}
	allocation := economics.AllocationInput{HubProductID: f.hub, Basis: economics.BasisManual, Shares: map[kernel.ID]decimal.Decimal{f.edr: decimal.NewFromInt(1)}}
	bundle := economics.BundleInput{BundleKey: "new_bundle", Shares: map[kernel.ID]decimal.Decimal{f.edr: decimal.NewFromInt(1)}}
	_, a := f.svc.SaveField(f.ctx, f.fin, field)
	_, b := f.svc.SaveMetric(f.ctx, f.fin, metric)
	_, c := f.svc.SaveAllocationRule(f.ctx, f.fin, allocation)
	_, d := f.svc.SaveBundleRule(f.ctx, f.fin, bundle)
	for name, err := range map[string]error{"field": a, "metric": b, "allocation": c, "bundle": d} {
		if !errors.Is(err, kernel.ErrConflict) {
			t.Fatalf("undated %s accepted: %v", name, err)
		}
	}
	field.EffectiveFrom = kernel.DateOf(2026, 2, 1)
	metric.EffectiveFrom = field.EffectiveFrom
	allocation.EffectiveFrom = field.EffectiveFrom
	bundle.EffectiveFrom = field.EffectiveFrom
	if _, err := f.svc.SaveField(f.ctx, f.fin, field); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SaveMetric(f.ctx, f.fin, metric); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SaveAllocationRule(f.ctx, f.fin, allocation); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SaveBundleRule(f.ctx, f.fin, bundle); err != nil {
		t.Fatal(err)
	}
}
