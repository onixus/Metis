package economics_test

import (
	"errors"
	"testing"

	economics "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/kernel"
	"github.com/shopspring/decimal"
)

func TestEC08_EC11_ManualValueValidatesEffectiveTypeAndMoney(t *testing.T) {
	f := newFixture(t)
	for _, field := range []economics.FieldInput{
		{Key: "manual_cash", Name: "Synthetic", Type: economics.FieldMoney, Source: economics.SourceManual, Currency: "RUB"},
		{Key: "manual_fx", Name: "Synthetic", Type: economics.FieldMoney, Source: economics.SourceManual, Currency: "USD"},
		{Key: "manual_date", Name: "Synthetic", Type: economics.FieldDate, Source: economics.SourceManual},
		{Key: "manual_ref", Name: "Synthetic", Type: economics.FieldReference, Source: economics.SourceManual},
		{Key: "future_only", Name: "Synthetic", Type: economics.FieldNumber, Source: economics.SourceManual, EffectiveFrom: kernel.DateOf(2027, 1, 1)},
	} {
		if _, err := f.svc.SaveField(f.ctx, f.fin, field); err != nil {
			t.Fatal(err)
		}
	}
	sl := economics.Slice{ProductID: f.edr, Period: jan()}
	for _, tc := range []struct {
		key   string
		value decimal.Decimal
	}{
		{"manual_cash", decimal.RequireFromString("1.5")},
		{"manual_cash", decimal.RequireFromString("9223372036854775808")},
		{"manual_fx", decimal.NewFromInt(100)},
		{"manual_date", decimal.NewFromInt(100)},
		{"manual_ref", decimal.NewFromInt(100)},
		{"future_only", decimal.NewFromInt(100)},
		{"headcount", decimal.New(1, 1000000)},
		{"headcount", decimal.New(1, -1000000)},
	} {
		if _, err := f.svc.SetManualValue(f.ctx, f.fin, tc.key, sl, tc.value); !errors.Is(err, kernel.ErrValidation) {
			t.Fatalf("accepted %s/%s: %v", tc.key, tc.value.Coefficient(), err)
		}
	}
	if _, err := f.svc.SetManualValue(f.ctx, f.fin, "headcount", economics.Slice{Period: jan()}, decimal.NewFromInt(1)); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("accepted productless fact: %v", err)
	}
	for _, raw := range []string{"9223372036854775807", "-9223372036854775808"} {
		if _, err := f.svc.SetManualValue(f.ctx, f.fin, "manual_cash", sl, decimal.RequireFromString(raw)); err != nil {
			t.Fatalf("valid boundary %s: %v", raw, err)
		}
	}
	// A future change of source must not reject manual input in the earlier period.
	if _, err := f.svc.SaveField(f.ctx, f.fin, economics.FieldInput{Key: "headcount", Name: "Future import", Type: economics.FieldNumber, Source: economics.SourceImport, EffectiveFrom: kernel.DateOf(2027, 1, 1)}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SetManualValue(f.ctx, f.fin, "headcount", sl, decimal.NewFromInt(5)); err != nil {
		t.Fatalf("ignored effective version: %v", err)
	}
}
