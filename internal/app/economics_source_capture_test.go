package app

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/onixus/metis/internal/economics"
	modeling "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

type changingFormulaCRM struct {
	ports.CRM
	keys  []string
	calls atomic.Int64
}

func (c *changingFormulaCRM) Deals(context.Context) ([]ports.Deal, error) {
	call := c.calls.Add(1)
	out := []ports.Deal{}
	for _, key := range c.keys {
		out = append(out, ports.Deal{ExternalID: key, ProductKey: key, Outcome: ports.DealWon, ClosedDate: kernel.DateOf(2026, 9, 1), Amount: kernel.RUB(call * 10)})
	}
	return out, nil
}

func TestEC09_EC13_FormulaSlicesShareCompleteCRMInput(t *testing.T) {
	for _, portfolioFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "portfolio-first", false: "products-first"}[portfolioFirst], func(t *testing.T) {
			a, _, book, other := formulaSourceApp(t)
			ctx := context.Background()
			sc := identityaccess.FinanceServiceScope("coherent-source")
			period, _ := modeling.ParsePeriod(book.Period)
			a.Cfg.FormulaBookProductID = book.ProductID.String()
			first, err := a.Portfolio.Product(ctx, sc, book.ProductID)
			if err != nil {
				t.Fatal(err)
			}
			second, err := a.Portfolio.Product(ctx, sc, other)
			if err != nil {
				t.Fatal(err)
			}
			crm := &changingFormulaCRM{keys: []string{first.Key, second.Key}}
			a.CRM = crm
			products := `crm_revenue("product","` + book.ProductID.String() + `")+crm_revenue("product","` + other.String() + `")`
			expression := `crm_revenue()-(` + products + `)`
			if !portfolioFirst {
				expression = `(` + products + `)-crm_revenue()`
			}
			if _, err := a.Modeling.SaveMetric(ctx, sc, modeling.MetricInput{Key: "coherent_source", Name: "Coherent source", Expression: expression, Currency: "RUB", EffectiveFrom: period.Start()}); err != nil {
				t.Fatal(err)
			}
			for operation := int64(1); operation <= 2; operation++ {
				value, err := a.Modeling.Value(ctx, sc, "coherent_source", modeling.Slice{Period: period})
				if err != nil || !value.IsZero() || crm.calls.Load() != operation {
					t.Fatalf("operation %d: result %s err %v raw reads %d", operation, value, err, crm.calls.Load())
				}
			}
		})
	}
}

func TestEC09_EC13_FormulaSlicesShareWorklogUniverseAndBookVersion(t *testing.T) {
	a, _, book, other := formulaSourceApp(t)
	ctx := context.Background()
	sc := identityaccess.FinanceServiceScope("coherent-worklogs")
	period, _ := modeling.ParsePeriod(book.Period)
	a.Cfg.FormulaBookProductID = book.ProductID.String()
	tracker := a.Tracker.(*sourceTracker)
	expression := `worklogs("product","` + book.ProductID.String() + `")+worklogs("product","` + other.String() + `")-worklogs()`
	if _, err := a.Modeling.SaveMetric(ctx, sc, modeling.MetricInput{Key: "coherent_worklogs", Name: "Coherent worklogs", Expression: expression, EffectiveFrom: period.Start()}); err != nil {
		t.Fatal(err)
	}
	value, err := a.Modeling.Value(ctx, sc, "coherent_worklogs", modeling.Slice{Period: period})
	if err != nil || !value.IsZero() || tracker.calls != 1 {
		t.Fatalf("worklogs result %s err %v reads %d", value, err, tracker.calls)
	}
	session := modeling.WithFormulaSnapshot(ctx)
	reader := economicsPlatformSources{app: a}
	sl := modeling.Slice{ProductID: book.ProductID, Period: period}
	first, err := reader.ReadFormulaSource(session, sc, modeling.SourceHeadcount, sl)
	if err != nil {
		t.Fatal(err)
	}
	book.Rows[0].Headcount = 7
	if _, err := a.Economics.Save(ctx, sc, economics.SaveInput{ProductID: book.ProductID, Period: book.Period, Currency: book.Currency, ExpectedVersion: book.Version, Rows: book.Rows, Fields: book.Fields}); err != nil {
		t.Fatal(err)
	}
	pinned, err := reader.ReadFormulaSource(session, sc, modeling.SourceHeadcount, sl)
	if err != nil || !pinned.Value.Equal(first.Value) || pinned.SnapshotID != first.SnapshotID {
		t.Fatalf("session drift: before %+v after %+v err %v", first, pinned, err)
	}
	fresh, err := reader.ReadFormulaSource(modeling.WithFormulaSnapshot(ctx), sc, modeling.SourceHeadcount, sl)
	if err != nil || fresh.Value.String() != "7" {
		t.Fatalf("new operation stale: %+v %v", fresh, err)
	}
}
