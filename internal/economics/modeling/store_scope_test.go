package economics_test

import (
	"context"
	"errors"
	"testing"

	economics "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

func TestAD02_NFS01_ModelFactsPreserveProductAndFinanceBoundary(t *testing.T) {
	ctx := context.Background()
	store := economics.NewMemStore()
	own, other := kernel.NewID(), kernel.NewID()
	admin := identityaccess.FinanceServiceScope("fixture")
	batch := economics.ImportBatch{ID: kernel.NewID(), Status: economics.BatchApplied, DataVersion: 1}
	if err := store.SaveBatch(ctx, admin, batch); err != nil {
		t.Fatal(err)
	}
	rows := []economics.FactRow{{ID: kernel.NewID(), BatchID: batch.ID, ProductID: own, DataVersion: 1}, {ID: kernel.NewID(), BatchID: batch.ID, ProductID: other, DataVersion: 1}}
	if err := store.AppendFacts(ctx, admin, rows); err != nil {
		t.Fatal(err)
	}
	scoped := authz.New(authz.Params{Subject: "reader", Products: map[kernel.ID]authz.Access{own: authz.AccessStrategic}, Finance: authz.FinanceFull})
	got, err := store.Facts(ctx, scoped, economics.FactFilter{})
	if err != nil || len(got) != 1 || got[0].ProductID != own {
		t.Fatalf("filtered facts: %+v %v", got, err)
	}
	if _, err := store.Facts(ctx, scoped, economics.FactFilter{Product: &other}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("foreign filter: %v", err)
	}
	aggregate := authz.New(authz.Params{Subject: "aggregate", Products: map[kernel.ID]authz.Access{own: authz.AccessStrategic}, Finance: authz.FinanceAggregates})
	if _, err := store.Facts(ctx, aggregate, economics.FactFilter{}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("raw finance: %v", err)
	}
	calculated := identityaccess.ModelCalculationScope(aggregate)
	got, err = store.Facts(ctx, calculated, economics.FactFilter{})
	if err != nil || len(got) != 1 || got[0].ProductID != own {
		t.Fatalf("calculation widened products: %+v %v", got, err)
	}
	if err := store.AppendFacts(ctx, calculated, rows[:1]); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("calculation gained writes: %v", err)
	}
}

func TestNFS16_ModelingTeamPayrollRefusesAggregateAndNonFinance(t *testing.T) {
	ctx := context.Background()
	svc, err := economics.NewService(economics.NewMemStore(), nil, kernel.SystemClock{}, economics.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, sc := range []authz.Scope{
		authz.New(authz.Params{Subject: "aggregate", Roles: []authz.Role{authz.RoleCPO}, AllProducts: authz.AccessPrivate, Finance: authz.FinanceAggregates}),
		authz.New(authz.Params{Subject: "full-non-finance", Roles: []authz.Role{authz.RoleCPO}, AllProducts: authz.AccessPrivate, Finance: authz.FinanceFull}),
	} {
		if _, err := svc.TeamCosts(ctx, sc, kernel.NewID(), economics.PeriodOf(2026, 1)); !errors.Is(err, kernel.ErrForbidden) {
			t.Fatalf("team payroll: %v", err)
		}
		if _, err := svc.Matrix(ctx, sc, economics.PeriodOf(2026, 1)); !errors.Is(err, kernel.ErrForbidden) {
			t.Fatalf("payroll matrix: %v", err)
		}
	}
}
