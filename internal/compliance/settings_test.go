package compliance_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onixus/metis/internal/compliance"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

func TestPR05_CM03_SettingsSurviveServiceRestartAndCannotBeMutatedByCaller(t *testing.T) {
	f := newFixture(t)
	st := compliance.DefaultSettings()
	st.CostByClass[compliance.ImpactAnalysisRequired] = kernel.RUB(120_000_00)
	st.BaselineLifetimeYears = 3
	if err := f.svc.UpdateSettings(f.ctx, adminScope(), st); err != nil {
		t.Fatal(err)
	}
	st.CostByClass[compliance.ImpactAnalysisRequired] = kernel.RUB(1)
	other := compliance.NewService(f.store, f.evidence, f.graph, f.pub, kernel.SystemClock{})
	got, err := other.Settings(f.ctx, f.cmp)
	if err != nil || got.CostByClass[compliance.ImpactAnalysisRequired].Amount != 120_000_00 || got.BaselineLifetimeYears != 3 {
		t.Fatalf("settings after restart: %+v, %v", got, err)
	}
	got.CostByClass[compliance.ImpactAnalysisRequired] = kernel.RUB(2)
	got, err = f.svc.Settings(f.ctx, f.cmp)
	if err != nil || got.CostByClass[compliance.ImpactAnalysisRequired].Amount != 120_000_00 {
		t.Fatalf("read exposed mutable settings: %+v, %v", got, err)
	}
	if _, err := other.SetImpactClass(f.ctx, f.cmp, f.agentFeature, f.agent, compliance.ImpactAnalysisRequired, "Анализ нового протокола"); err != nil {
		t.Fatal(err)
	}
	cost, err := other.ConfirmationCost(f.ctx, f.cmp, f.agentFeature)
	if err != nil || cost != kernel.RUB(60_000_00) {
		t.Fatalf("confirmation cost must use persisted settings and 50%% discount: %+v, %v", cost, err)
	}
}

func TestNFS01_CM03_SettingsDenyZeroScopeAndNonAdminWrites(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.Settings(f.ctx, authz.Scope{}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("zero scope read: %v", err)
	}
	if _, err := f.store.Settings(f.ctx, authz.Scope{}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("zero scope store read: %v", err)
	}
	for _, sc := range []authz.Scope{{}, f.cmp, pmScope(f.agent), presaleScope()} {
		if err := f.store.SaveSettings(f.ctx, sc, compliance.DefaultSettings()); !errors.Is(err, kernel.ErrForbidden) {
			t.Fatalf("unauthorized settings write: %v", err)
		}
	}
}

func TestPR05_SettingsRequireAllImpactCostsAndCurrency(t *testing.T) {
	f := newFixture(t)
	st := compliance.DefaultSettings()
	delete(st.CostByClass, compliance.ImpactSecurityFunctions)
	if err := f.svc.UpdateSettings(f.ctx, adminScope(), st); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("missing cost silently becomes zero: %v", err)
	}
	st = compliance.DefaultSettings()
	st.CostByClass[compliance.ImpactSecurityFunctions] = kernel.Money{Amount: 100}
	if err := f.svc.UpdateSettings(f.ctx, adminScope(), st); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("missing currency: %v", err)
	}
}

type unavailableSettingsStore struct{ compliance.Store }

func (unavailableSettingsStore) Settings(context.Context, authz.Scope) (compliance.Settings, error) {
	return compliance.Settings{}, context.DeadlineExceeded
}

func TestPR05_SettingsReadFailureDoesNotSilentlyChangeCost(t *testing.T) {
	f := newFixture(t)
	svc := compliance.NewService(unavailableSettingsStore{f.store}, f.evidence, f.graph, f.pub, kernel.SystemClock{})
	if _, err := svc.Settings(f.ctx, f.cmp); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("settings storage failure must propagate: %v", err)
	}
	if _, err := svc.ConfirmationCost(f.ctx, f.cmp, f.agentFeature); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cost must not use defaults on storage failure: %v", err)
	}
}
