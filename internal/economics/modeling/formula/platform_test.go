package formula_test

import (
	"fmt"
	"testing"

	"github.com/onixus/metis/internal/economics/modeling/formula"
	"github.com/shopspring/decimal"
)

type platformStub struct {
	stubSource
	calls []formula.Filter
	kinds []string
}

func (s *platformStub) Platform(kind string, filter formula.Filter) (decimal.Decimal, error) {
	s.calls = append(s.calls, filter)
	s.kinds = append(s.kinds, kind)
	switch kind {
	case "worklogs":
		return dec("3600"), nil
	case "crm_revenue":
		return dec("9007199254740993"), nil
	case "headcount":
		return dec("5"), nil
	}
	return decimal.Zero, fmt.Errorf("unknown source")
}

func TestEC09_EC10_TypedSourceFunctionsAndDependencies(t *testing.T) {
	engine, _ := formula.New()
	program, err := engine.Compile(`crm_revenue() + worklogs("team", "*") / headcount()`)
	if err != nil {
		t.Fatal(err)
	}
	refs := program.Refs()
	if len(refs) != 3 {
		t.Fatalf("source refs: %+v", refs)
	}
	for _, ref := range refs {
		if ref.Kind != formula.RefSource {
			t.Fatalf("source became field: %+v", ref)
		}
	}
	source := &platformStub{}
	got, err := program.Eval(source, formula.Slice{Product: "product", Team: "team", Period: "2026-01"})
	if err != nil || !got.Equal(dec("9007199254741713")) {
		t.Fatalf("exact result: %v %v", got, err)
	}
	if len(source.calls) != 3 || source.calls[1].Dim != formula.DimTeam || source.calls[1].Value != "*" {
		t.Fatalf("filter lost: %+v", source.calls)
	}
	for _, expr := range []string{`worklogs("bad", "*")`, `worklogs("period", "*")`, `headcount(product, "*")`, `crm_revenue("product", team)`} {
		if _, err := engine.Compile(expr); err == nil {
			t.Fatalf("untyped source args accepted: %s", expr)
		}
	}
	missing, err := engine.Compile(`headcount()`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := missing.Eval(&stubSource{}, formula.Slice{}); err == nil {
		t.Fatal("missing typed source silently zero")
	}
}

func TestEC09_EC11_LegacyDivisionAndBankersMinorParity(t *testing.T) {
	engine, _ := formula.New()
	for _, test := range []struct{ expression, want string }{
		{`div16(d("1"),d("3"))`, "0.3333333333333333"},
		{`round_minor(d("2.5"))`, "2"},
		{`round_minor(d("3.5"))`, "4"},
		{`round_minor(d("-2.5"))`, "-2"},
		{`round_minor(div16(d("125"),d("2")))`, "62"},
		{`round_minor(d("9007199254740993.5"))`, "9007199254740994"},
	} {
		program, err := engine.Compile(test.expression)
		if err != nil {
			t.Fatal(err)
		}
		got, err := program.Eval(&stubSource{}, formula.Slice{})
		if err != nil || !got.Equal(dec(test.want)) {
			t.Fatalf("%s: %v %v want%s", test.expression, got, err, test.want)
		}
		if len(program.Refs()) != 0 {
			t.Fatalf("pure operation created dependency: %+v", program.Refs())
		}
	}
	for _, expression := range []string{`div16(d("1"),d("0"))`, `round_minor(d("1e100000000"))`} {
		program, err := engine.Compile(expression)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := program.Eval(&stubSource{}, formula.Slice{}); err == nil {
			t.Fatalf("unbounded/zero operation accepted: %s", expression)
		}
	}
}

func TestEC09_EC11_PurePairHelpersKeepHistoricalPrecisionAndLinearSize(t *testing.T) {
	engine, _ := formula.New()
	for _, tc := range []struct{ expression, want string }{
		{`min_pair(d("9007199254740993.001"),d("9007199254740993.002"))`, "9007199254740993.001"},
		{`max_pair(d("-0.001"),d("-0.002"))`, "-0.001"},
		{`min_pair(max_pair(d("7"),d("2")),d("4"))`, "4"},
	} {
		program, err := engine.Compile(tc.expression)
		if err != nil {
			t.Fatal(err)
		}
		actual, err := program.Eval(&stubSource{}, formula.Slice{})
		if err != nil || actual.String() != tc.want {
			t.Fatalf("pairhelper: %s => %v %v", tc.expression, actual, err)
		}
		if len(program.Refs()) != 0 {
			t.Fatalf("pair helper introduced references: %+v", program.Refs())
		}
	}
}
