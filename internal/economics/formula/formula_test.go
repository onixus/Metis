package formula_test

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/economics/formula"
)

// stubSource — источник данных для тестов движка.
type stubSource struct {
	fields  map[string][]decimal.Decimal
	metrics map[string]decimal.Decimal
	lastF   formula.Filter
}

func (s *stubSource) Values(key string, f formula.Filter) ([]decimal.Decimal, error) {
	s.lastF = f
	return s.fields[key], nil
}

func (s *stubSource) Metric(key string, _ formula.Slice) (decimal.Decimal, error) {
	return s.metrics[key], nil
}

func dec(s string) decimal.Decimal {
	d, err := decimal.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return d
}

// TestEC09_FormulaArithmeticInDecimal: формула считает в decimal, без потери точности float.
func TestEC09_FormulaArithmeticInDecimal(t *testing.T) {
	eng, err := formula.New()
	if err != nil {
		t.Fatalf("движок: %v", err)
	}
	src := &stubSource{fields: map[string][]decimal.Decimal{
		"revenue": {dec("0.1"), dec("0.2")},
		"fot":     {dec("1000000")},
	}}
	p, err := eng.Compile(`field("revenue") - d("0.3")`)
	if err != nil {
		t.Fatalf("компиляция: %v", err)
	}
	got, err := p.Eval(src, formula.Slice{Product: "p1", Period: "2026-01"})
	if err != nil {
		t.Fatalf("вычисление: %v", err)
	}
	if !got.IsZero() {
		t.Fatalf("0.1 + 0.2 - 0.3 должно быть 0, получено %s", got)
	}
}

// TestEC09_MixedLiteralArithmetic: коэффициенты-литералы допустимы с любой стороны.
func TestEC09_MixedLiteralArithmetic(t *testing.T) {
	eng, _ := formula.New()
	src := &stubSource{fields: map[string][]decimal.Decimal{"fot": {dec("200")}}}
	for _, expr := range []string{`d("0.5") * field("fot")`, `field("fot") * 0.5`, `field("fot") / 2`} {
		p, err := eng.Compile(expr)
		if err != nil {
			t.Fatalf("компиляция %q: %v", expr, err)
		}
		got, err := p.Eval(src, formula.Slice{})
		if err != nil {
			t.Fatalf("вычисление %q: %v", expr, err)
		}
		if !got.Equal(dec("100")) {
			t.Fatalf("%q: ожидалось 100, получено %s", expr, got)
		}
	}
}

// TestEC09_NoArbitraryCode: в формуле нет макросов, списков и обращений к внешнему миру.
func TestEC09_NoArbitraryCode(t *testing.T) {
	eng, _ := formula.New()
	for _, expr := range []string{
		`[1,2,3].exists(x, x > 1) ? d("1") : d("0")`,
		`has(field)`,
		`"a".size()`,
		`timestamp("2026-01-01T00:00:00Z")`,
		`field(product)`,
		`0.5 * field("fot")`, // decimal обязан стоять слева: литерал пишется справа или через d()
	} {
		if _, err := eng.Compile(expr); err == nil {
			t.Fatalf("формула %q должна быть отклонена", expr)
		}
	}
}

// TestEC10_RefsCollectedForDependencyGraph: ссылки формулы собираются для графа зависимостей.
func TestEC10_RefsCollectedForDependencyGraph(t *testing.T) {
	eng, _ := formula.New()
	p, err := eng.Compile(`field("revenue") - metric("cost") + sum("fot", "product", "*")`)
	if err != nil {
		t.Fatalf("компиляция: %v", err)
	}
	refs := p.Refs()
	want := map[formula.Ref]bool{
		{Kind: formula.RefField, Key: "revenue"}: true,
		{Kind: formula.RefField, Key: "fot"}:     true,
		{Kind: formula.RefMetric, Key: "cost"}:   true,
	}
	if len(refs) != len(want) {
		t.Fatalf("ссылок %d, ожидалось %d: %+v", len(refs), len(want), refs)
	}
	for _, r := range refs {
		if !want[r] {
			t.Fatalf("неожиданная ссылка %+v", r)
		}
	}
}

// TestEC09_AggregateByDimension: агрегат раскрывает измерение среза.
func TestEC09_AggregateByDimension(t *testing.T) {
	eng, _ := formula.New()
	src := &stubSource{fields: map[string][]decimal.Decimal{"revenue": {dec("10"), dec("20")}}}
	p, err := eng.Compile(`sum("revenue", "product", "*")`)
	if err != nil {
		t.Fatalf("компиляция: %v", err)
	}
	got, err := p.Eval(src, formula.Slice{Product: "p1", Period: "2026-01"})
	if err != nil {
		t.Fatalf("вычисление: %v", err)
	}
	if !got.Equal(dec("30")) {
		t.Fatalf("ожидалось 30, получено %s", got)
	}
	if src.lastF.Dim != formula.DimProduct || src.lastF.Value != formula.Any {
		t.Fatalf("фильтр не раскрыл измерение: %+v", src.lastF)
	}
}

// TestEC09_CostLimitStopsExpensiveFormula: стоимость вычисления ограничена сверху.
func TestEC09_CostLimitStopsExpensiveFormula(t *testing.T) {
	eng, _ := formula.New()
	cheap := eng.WithCostLimit(1)
	p, err := cheap.Compile(`field("revenue") + field("revenue") + field("revenue")`)
	if err != nil {
		t.Fatalf("компиляция: %v", err)
	}
	if _, err := p.Eval(&stubSource{}, formula.Slice{}); err == nil {
		t.Fatal("вычисление должно упереться в предел стоимости")
	}
}

// TestEC09_DivisionByZeroIsError: деление на ноль — ошибка, а не паника.
func TestEC09_DivisionByZeroIsError(t *testing.T) {
	eng, _ := formula.New()
	p, err := eng.Compile(`field("revenue") / field("zero")`)
	if err != nil {
		t.Fatalf("компиляция: %v", err)
	}
	_, err = p.Eval(&stubSource{fields: map[string][]decimal.Decimal{"revenue": {dec("1")}}}, formula.Slice{})
	if err == nil || !strings.Contains(err.Error(), "ноль") {
		t.Fatalf("ожидалась ошибка деления на ноль, получено %v", err)
	}
}
