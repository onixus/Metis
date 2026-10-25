package economics_test

import (
	"context"
	"errors"
	"testing"

	economics "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/kernel"
	"github.com/shopspring/decimal"
)

func TestEC08_EC13_ScenarioRejectsInvalidOverridesWithoutChangingFacts(t *testing.T) {
	f := newFixture(t)
	f.loadControlData()
	before, err := f.svc.ProductPnL(f.ctx, f.fin, f.edr, jan())
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []economics.FieldInput{
		{Key: "a_date", Name: "Synthetic", Type: economics.FieldDate, Source: economics.SourceManual},
		{Key: "a_ref", Name: "Synthetic", Type: economics.FieldReference, Source: economics.SourceImport},
		{Key: "a_calc", Name: "Synthetic", Type: economics.FieldNumber, Source: economics.SourceCalculated},
		{Key: "a_future", Name: "Synthetic", Type: economics.FieldNumber, Source: economics.SourceManual, EffectiveFrom: kernel.DateOf(2027, 1, 1)},
	} {
		if _, err := f.svc.SaveField(f.ctx, f.fin, field); err != nil {
			t.Fatal(err)
		}
	}
	base := economics.Override{FieldKey: "revenue", ProductID: f.edr, Period: jan(), Value: decimal.NewFromInt(100)}
	cases := []economics.Override{
		{FieldKey: "headcount", Value: decimal.New(1, 100000000)},
		{FieldKey: "revenue", Value: decimal.RequireFromString("1.5")},
		{FieldKey: "revenue", Value: decimal.RequireFromString("9223372036854775808")},
		{FieldKey: "revenue", ProductID: f.vm, Value: decimal.NewFromInt(100)},
		{FieldKey: "revenue", Period: economics.PeriodOf(2027, 1), Value: decimal.NewFromInt(100)},
		{FieldKey: "a_date"}, {FieldKey: "a_ref"}, {FieldKey: "a_calc"}, {FieldKey: "a_future"},
	}
	for i, ov := range cases {
		if _, err := f.svc.SaveScenario(f.ctx, f.fin, economics.Scenario{Name: "Synthetic", Period: jan(), Products: []kernel.ID{f.edr}, Overrides: []economics.Override{ov}}); !errors.Is(err, kernel.ErrValidation) {
			t.Fatalf("case %d accepted invalid override: %v", i, err)
		}
	}
	for _, ovs := range [][]economics.Override{{base, base}, {base, {FieldKey: "revenue", Value: decimal.NewFromInt(10)}}} {
		if _, err := f.svc.SaveScenario(f.ctx, f.fin, economics.Scenario{Name: "Synthetic", Period: jan(), Overrides: ovs}); !errors.Is(err, kernel.ErrValidation) {
			t.Fatalf("overlapping overrides allowed: %v", err)
		}
	}
	sn, err := f.svc.SaveScenario(f.ctx, f.fin, economics.Scenario{Name: "Synthetic", Period: jan(), Products: []kernel.ID{f.edr}, Overrides: []economics.Override{base}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.RunScenario(f.ctx, f.fin, sn.ID, nil); err != nil {
		t.Fatal(err)
	}
	after, err := f.svc.ProductPnL(f.ctx, f.fin, f.edr, jan())
	if err != nil || before != after {
		t.Fatalf("scenario changed facts: before=%+v after=%+v err=%v", before, after, err)
	}
}

func TestEC13_StoredScenarioRejectsUnboundedDecimalBeforeArithmetic(t *testing.T) {
	ctx, sc := context.Background(), financeScope()
	store := economics.NewMemStore()
	svc, err := economics.NewService(store, nil, kernel.SystemClock{}, economics.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	sn := economics.Scenario{ID: kernel.NewID(), Name: "Synthetic legacy", Period: jan(), Overrides: []economics.Override{{FieldKey: "headcount", Value: decimal.New(1, 100000000)}}}
	if err := store.SaveScenario(ctx, sc, sn); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RunScenario(ctx, sc, sn.ID, nil); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("legacy unbounded decimal accepted: %v", err)
	}
}
