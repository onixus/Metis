//go:build integration

package app

import (
	"context"
	"testing"

	"github.com/onixus/metis/internal/economics"
	modeling "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/portfoliograph"
)

func TestEC09_EC13_NFS05_PostgresFormulaSlicesShareRawCapture(t *testing.T) {
	isolateSourceRuntimeDatabase(t)
	a, _, _ := runtimeApps(t)
	ctx := context.Background()
	setup := identityaccess.ServiceScope("capture-setup")
	sc := identityaccess.FinanceServiceScope("capture-finance")
	period := modeling.PeriodOf(2026, 9)
	var products []portfoliograph.Product
	if err := a.runOperation(ctx, func(ctx context.Context) error {
		for _, key := range []string{"capture-first", "capture-second"} {
			product, err := a.Portfolio.CreateProduct(ctx, setup, portfoliograph.ProductInput{Key: key, Name: "Synthetic capture", Type: portfoliograph.ProductTypeSecurity})
			if err != nil {
				return err
			}
			products = append(products, product)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	a.Cfg.FormulaBookProductID = products[0].ID.String()
	a.CRM = &changingFormulaCRM{keys: []string{products[0].Key, products[1].Key}}
	expression := `crm_revenue()-crm_revenue("product","` + products[0].ID.String() + `")-crm_revenue("product","` + products[1].ID.String() + `")`
	if err := a.runOperation(ctx, func(ctx context.Context) error {
		_, err := a.Economics.Save(ctx, sc, economics.SaveInput{ProductID: products[0].ID, Period: period.String(), Currency: "RUB", Rows: []economics.Row{{ProductID: products[0].ID, Category: economics.Revenue, Amount: kernel.RUB(10)}, {ProductID: products[1].ID, Category: economics.Revenue, Amount: kernel.RUB(10)}}})
		if err != nil {
			return err
		}
		_, err = a.Modeling.SaveMetric(ctx, sc, modeling.MetricInput{Key: "coherent_pg", Name: "Coherent capture", Expression: expression, Currency: "RUB", EffectiveFrom: period.Start()})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.runOperation(ctx, func(ctx context.Context) error {
		value, err := a.Modeling.Value(ctx, sc, "coherent_pg", modeling.Slice{Period: period})
		if err != nil {
			return err
		}
		if !value.IsZero() {
			t.Errorf("incoherent result %s", value)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if calls := a.CRM.(*changingFormulaCRM).calls.Load(); calls != 1 {
		t.Fatalf("raw CRM reads %d", calls)
	}
}
