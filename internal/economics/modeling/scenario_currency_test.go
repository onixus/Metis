package economics_test

import (
	"context"
	"errors"
	"testing"

	economics "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/kernel"
	"github.com/shopspring/decimal"
)

func TestDA02_EC13_ScenarioRejectsBudgetCurrencyMismatch(t *testing.T) {
	f := newFixture(t)
	for _, money := range []kernel.Money{{Amount: -100, Currency: "USD"}, {Amount: 100}, {Currency: "EUR"}} {
		if _, err := f.svc.SaveScenario(f.ctx, f.fin, economics.Scenario{Name: "Synthetic", Period: jan(), BudgetDelta: money}); !errors.Is(err, kernel.ErrValidation) {
			t.Fatalf("accepted invalid budget %+v: %v", money, err)
		}
	}
	for _, money := range []kernel.Money{{}, {Amount: -100, Currency: "RUB"}, {Currency: "RUB"}} {
		if _, err := f.svc.SaveScenario(f.ctx, f.fin, economics.Scenario{Name: "Synthetic", Period: jan(), BudgetDelta: money}); err != nil {
			t.Fatalf("rejected valid budget %+v: %v", money, err)
		}
	}
}

func TestDA02_EC13_StoredScenarioCurrencyValidatedAtRun(t *testing.T) {
	ctx := context.Background()
	store := economics.NewMemStore()
	svc, err := economics.NewService(store, nil, kernel.SystemClock{}, economics.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	scenario := economics.Scenario{ID: kernel.NewID(), Name: "Legacy synthetic", Period: jan(), BudgetDelta: kernel.Money{Amount: 100, Currency: "USD"}}
	if err := store.SaveScenario(ctx, financeScope(), scenario); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RunScenario(ctx, financeScope(), scenario.ID, nil); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("legacy scenario currency accepted: %v", err)
	}
}

func TestDA02_EC13_TrackCurrencyValidationPreservesFacts(t *testing.T) {
	f := newFixture(t)
	f.loadControlData()
	before, err := f.svc.ProductPnL(f.ctx, f.fin, f.edr, jan())
	if err != nil {
		t.Fatal(err)
	}
	sn, err := f.svc.SaveScenario(f.ctx, f.fin, economics.Scenario{Name: "Synthetic", Period: jan(), Products: []kernel.ID{f.edr}})
	if err != nil {
		t.Fatal(err)
	}
	for _, cost := range []kernel.Money{{Amount: 100, Currency: "USD"}, {Amount: 100}, {Currency: "EUR"}} {
		f.svc.WithTracks(tracksStub{items: []economics.TrackRef{{ID: kernel.NewID(), ProductID: f.edr, PlannedCost: cost}}})
		if _, err := f.svc.RunScenario(f.ctx, f.fin, sn.ID, nil); !errors.Is(err, kernel.ErrValidation) {
			t.Fatalf("accepted track cost %+v: %v", cost, err)
		}
	}
	for _, cost := range []kernel.Money{{}, {Amount: 100, Currency: "RUB"}} {
		f.svc.WithTracks(tracksStub{items: []economics.TrackRef{{ID: kernel.NewID(), ProductID: f.edr, PlannedCost: cost}}})
		if _, err := f.svc.RunScenario(f.ctx, f.fin, sn.ID, nil); err != nil {
			t.Fatalf("rejected valid track cost %+v: %v", cost, err)
		}
	}
	after, err := f.svc.ProductPnL(f.ctx, f.fin, f.edr, jan())
	if err != nil || before != after {
		t.Fatalf("scenario changed actual facts: before=%+v after=%+v err=%v", before, after, err)
	}
}

func TestDA02_EC13_ScenarioBudgetUsesEffectiveFieldCurrency(t *testing.T) {
	f := newFixture(t)
	f.svc.WithTracks(tracksStub{items: []economics.TrackRef{{ID: kernel.NewID(), ProductID: f.edr, PlannedCost: kernel.RUB(100)}}})
	if _, err := f.svc.SaveField(f.ctx, f.fin, economics.FieldInput{Key: "budget", Name: "Synthetic USD budget", Type: economics.FieldMoney, Source: economics.SourceImport, Currency: "USD", EffectiveFrom: kernel.DateOf(2026, 2, 1)}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		period  economics.Period
		wantErr bool
	}{{jan(), false}, {economics.PeriodOf(2026, 2), true}} {
		sn, err := f.svc.SaveScenario(f.ctx, f.fin, economics.Scenario{Name: "Synthetic", Period: tc.period, Products: []kernel.ID{f.edr}})
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.svc.RunScenario(f.ctx, f.fin, sn.ID, nil)
		if tc.wantErr && !errors.Is(err, kernel.ErrValidation) || !tc.wantErr && err != nil {
			t.Fatalf("period %s validation: %v", tc.period, err)
		}
	}
}

func TestDA02_EC13_VirtualBudgetOverrideFundsTracksWithoutChangingFacts(t *testing.T) {
	f := newFixture(t)
	f.loadControlData()
	f.svc.WithTracks(tracksStub{items: []economics.TrackRef{{ID: kernel.NewID(), ProductID: f.edr, PlannedCost: kernel.RUB(100)}}})
	before, err := f.svc.ProductPnL(f.ctx, f.fin, f.edr, jan())
	if err != nil {
		t.Fatal(err)
	}
	sn, err := f.svc.SaveScenario(f.ctx, f.fin, economics.Scenario{Name: "Synthetic virtual budget", Period: jan(), Products: []kernel.ID{f.edr}, Overrides: []economics.Override{{FieldKey: "budget", ProductID: f.edr, Value: decimal.NewFromInt(200)}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.svc.RunScenario(f.ctx, f.fin, sn.ID, nil)
	if err != nil || len(result.Tracks) != 1 || result.Tracks[0].AtRisk {
		t.Fatalf("virtual budget ignored: %+v %v", result.Tracks, err)
	}
	after, err := f.svc.ProductPnL(f.ctx, f.fin, f.edr, jan())
	if err != nil || before != after {
		t.Fatalf("scenario changed facts: %+v %+v %v", before, after, err)
	}
}
