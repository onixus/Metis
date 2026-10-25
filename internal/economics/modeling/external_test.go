package economics_test

import (
	"context"
	"errors"
	"testing"

	economics "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/shopspring/decimal"
)

type externalAuditor struct{ err error }

func (a externalAuditor) FinanceAccess(context.Context, string, string, string, kernel.ID, map[string]any) error {
	return a.err
}

func TestEC09_EC10_NFS01_ExternalFactsRequireAuditAndScope(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.SaveMetric(f.ctx, f.fin, economics.MetricInput{Key: "external", Name: "External", Expression: `field("revenue")`})
	if err != nil {
		t.Fatal(err)
	}
	period := economics.PeriodOf(2026, 2)
	facts := []economics.FactRow{{ID: kernel.NewID(), ProductID: f.edr, Period: period, FieldKey: "revenue", Value: decimal.NewFromInt(123)}}
	sl := economics.Slice{ProductID: f.edr, Period: period}
	if _, err := f.svc.EvaluateExternal(f.ctx, f.fin, "external", sl, facts, map[string]economics.FieldType{"revenue": economics.FieldMoney}); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("missing audit allowed: %v", err)
	}
	failure := errors.New("audit failed")
	f.svc.WithAuditor(externalAuditor{err: failure})
	if _, err := f.svc.EvaluateExternal(f.ctx, f.fin, "external", sl, facts, map[string]economics.FieldType{"revenue": economics.FieldMoney}); !errors.Is(err, failure) {
		t.Fatalf("audit failure hidden: %v", err)
	}
	f.svc.WithAuditor(externalAuditor{})
	if _, err := f.svc.EvaluateExternal(f.ctx, authz.Scope{}, "external", sl, facts, map[string]economics.FieldType{"revenue": economics.FieldMoney}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("zero scope allowed: %v", err)
	}
	restricted := authz.New(authz.Params{Subject: "restricted", Roles: []authz.Role{authz.RoleFinance}, Finance: authz.FinanceFull, Products: map[kernel.ID]authz.Access{f.edr: authz.AccessPrivate}})
	facts[0].ProductID = f.vm
	if _, err := f.svc.EvaluateExternal(f.ctx, restricted, "external", sl, facts, map[string]economics.FieldType{"revenue": economics.FieldMoney}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("foreign facts allowed: %v", err)
	}
}

func TestEC09_EC11_ExternalFactsRejectOnlyReferencedCurrencyConflicts(t *testing.T) {
	for _, mode := range []string{"field", "nested", "unrelated"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			period := economics.PeriodOf(2026, 2)
			_, err := f.svc.SaveMetric(f.ctx, f.fin, economics.MetricInput{Key: "base_external", Name: "Base", Expression: `field("revenue")`, Currency: "RUB"})
			if err != nil {
				t.Fatal(err)
			}
			expression := `metric("base_external")`
			if mode == "nested" {
				_, err = f.svc.SaveMetric(f.ctx, f.fin, economics.MetricInput{Key: "base_external", Name: "Base", Expression: `field("revenue")`, Currency: "USD"})
			} else {
				key := "revenue"
				if mode == "unrelated" {
					key = "unused_usd"
				}
				_, err = f.svc.SaveField(f.ctx, f.fin, economics.FieldInput{Key: key, Name: key, Type: economics.FieldMoney, Source: economics.SourceImport, Currency: "USD"})
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.svc.SaveMetric(f.ctx, f.fin, economics.MetricInput{Key: "external_currency", Name: "External", Expression: expression, Currency: "RUB"})
			if err != nil {
				t.Fatal(err)
			}
			f.svc.WithAuditor(externalAuditor{})
			_, err = f.svc.EvaluateExternal(f.ctx, f.fin, "external_currency", economics.Slice{ProductID: f.edr, Period: period}, []economics.FactRow{{ProductID: f.edr, Period: period, FieldKey: "revenue", Value: decimal.NewFromInt(100)}}, map[string]economics.FieldType{"revenue": economics.FieldMoney})
			if mode == "unrelated" {
				if err != nil {
					t.Fatalf("unrelated currency rejected: %v", err)
				}
			} else if !errors.Is(err, kernel.ErrValidation) {
				t.Fatalf("mixed currency allowed: %v", err)
			}
		})
	}
}

func TestEC08_EC09_ExternalFactsRequireMatchingEffectiveTypes(t *testing.T) {
	for _, typ := range []economics.FieldType{economics.FieldMoney, economics.FieldNumber, economics.FieldPercent} {
		t.Run(string(typ), func(t *testing.T) {
			f := newFixture(t)
			if _, err := f.svc.SaveField(f.ctx, f.fin, economics.FieldInput{Key: "typed_input", Name: "Synthetic", Type: typ, Source: economics.SourceManual, Currency: "RUB"}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.svc.SaveMetric(f.ctx, f.fin, economics.MetricInput{Key: "typed_metric", Name: "Synthetic", Expression: `field("typed_input")`}); err != nil {
				t.Fatal(err)
			}
			f.svc.WithAuditor(externalAuditor{})
			sl := economics.Slice{ProductID: f.edr, Period: jan()}
			facts := []economics.FactRow{{ProductID: f.edr, Period: jan(), FieldKey: "typed_input", Value: decimal.NewFromInt(100)}}
			for _, externalType := range []economics.FieldType{economics.FieldMoney, economics.FieldNumber, economics.FieldPercent} {
				_, err := f.svc.EvaluateExternal(f.ctx, f.fin, "typed_metric", sl, facts, map[string]economics.FieldType{"typed_input": externalType})
				if typ == externalType && err != nil || typ != externalType && !errors.Is(err, kernel.ErrValidation) {
					t.Fatalf("model=%s book=%s: %v", typ, externalType, err)
				}
			}
			if _, err := f.svc.EvaluateExternal(f.ctx, f.fin, "typed_metric", sl, facts, nil); !errors.Is(err, kernel.ErrValidation) {
				t.Fatalf("untyped facts accepted: %v", err)
			}
		})
	}
}
