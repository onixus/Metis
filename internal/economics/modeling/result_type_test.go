package economics_test

import (
	"context"
	"errors"
	"testing"

	economics "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/kernel"
)

func TestEC09_EC11_ImportedMoneyResultTypeGuardsValueExplainAndCompare(t *testing.T) {
	ctx, sc := context.Background(), financeScope()
	for _, tc := range []struct {
		kind       economics.FieldType
		expression string
		wantError  bool
	}{
		{economics.FieldMoney, `d("1.5")`, true},
		{economics.FieldMoney, `d("9223372036854775808")`, true},
		{economics.FieldMoney, `d("9223372036854775807")`, false},
		{economics.FieldNumber, `d("1.5")`, false},
		{economics.FieldPercent, `d("1.5")`, false},
		{"", `d("1.5")`, false},
	} {
		store := economics.NewMemStore()
		metric := economics.Metric{ID: kernel.NewID(), Key: "typed_result", Versions: []economics.MetricVersion{{Version: 1, Name: "Original", Expression: `round_minor(d("1.5"))`, Currency: "RUB", ResultType: tc.kind}, {Version: 2, Name: "Edited", Expression: tc.expression, Currency: "RUB", ResultType: tc.kind}}}
		if err := store.SaveMetric(ctx, sc, metric); err != nil {
			t.Fatal(err)
		}
		service, err := economics.NewService(store, nil, kernel.SystemClock{}, economics.DefaultConfig())
		if err != nil {
			t.Fatal(err)
		}
		sl := economics.Slice{ProductID: kernel.NewID(), Period: jan()}
		_, valueErr := service.Value(ctx, sc, metric.Key, sl)
		_, explainErr := service.Explain(ctx, sc, metric.Key, sl)
		a, _, compareErr := service.CompareVersions(ctx, sc, metric.Key, sl, 1, 2)
		for _, got := range []error{valueErr, explainErr, compareErr} {
			if tc.wantError && !errors.Is(got, kernel.ErrValidation) {
				t.Fatalf("invalid money accepted %s: %v", tc.expression, got)
			}
			if !tc.wantError && got != nil {
				t.Fatalf("numeric result rejected %s/%s: %v", tc.kind, tc.expression, got)
			}
		}
		if !tc.wantError && a.String() != "2" {
			t.Fatalf("original bankers rounding changed: %s", a)
		}
	}
}
