package economics_test

import (
	"context"
	"testing"

	"github.com/onixus/metis/internal/economics"
	"github.com/shopspring/decimal"
)

func TestEC03_EC08_EC12_ProjectionRetainsExactAllocatedMoneyAndNumericInputs(t *testing.T) {
	svc, _, _, sc, in, _ := fixture(t)
	in.Fields = append(in.Fields, economics.Field{Key: "custom_money", Type: "money", Source: "manual"})
	in.Rows[2].Values["custom_money"] = "101"
	snap, err := svc.Save(context.Background(), sc, in)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := svc.Project(context.Background(), sc, in.ProductID, in.Period, 1)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Snapshot.ID != snap.ID {
		t.Fatal("snapshot identity lost")
	}
	totals := map[string]decimal.Decimal{}
	for _, fact := range projection.Facts {
		totals[fact.FieldKey] = totals[fact.FieldKey].Add(fact.Value)
		if fact.RowID == snap.Rows[2].ID && fact.FieldKey == "custom_money" && !fact.Value.Equal(decimal.NewFromInt(50)) && !fact.Value.Equal(decimal.NewFromInt(51)) {
			t.Fatalf("lost custom money cent: %+v", fact)
		}
		if fact.FieldKey == "margin" || fact.FieldKey == "profit_copy" || fact.FieldKey == "profit_twice" {
			t.Fatalf("calculated book fields became model inputs: %+v", fact)
		}
	}
	if !totals["custom_money"].Equal(decimal.NewFromInt(101)) || !totals["worklog_hours"].Equal(decimal.NewFromInt(80)) || !totals[economics.Payroll].Equal(decimal.NewFromInt(2001)) {
		t.Fatalf("projection lost value: %+v", totals)
	}
}
