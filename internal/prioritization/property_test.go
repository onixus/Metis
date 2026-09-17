package prioritization_test

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"pgregory.net/rapid"

	"github.com/onixus/metis/internal/kernel"
	pr "github.com/onixus/metis/internal/prioritization"
)

var varNames = []string{"a", "b", "reach", "effort", "arr", "x1"}

// genExpr порождает выражение по грамматике вычислителя с ограниченной глубиной.
func genExpr(rt *rapid.T, depth int) string {
	if depth <= 0 {
		if rapid.Bool().Draw(rt, "leafVar") {
			return rapid.SampledFrom(varNames).Draw(rt, "var")
		}
		return decimal.NewFromFloat(rapid.Float64Range(0, 1000).Draw(rt, "num")).Round(3).Abs().String()
	}
	switch rapid.IntRange(0, 5).Draw(rt, "kind") {
	case 0:
		return "(" + genExpr(rt, depth-1) + ")"
	case 1:
		return "-" + genExpr(rt, depth-1)
	case 2:
		return "min(" + genExpr(rt, depth-1) + ", " + genExpr(rt, depth-1) + ")"
	case 3:
		return "max(" + genExpr(rt, depth-1) + "," + genExpr(rt, depth-1) + ")"
	default:
		op := rapid.SampledFrom([]string{" + ", " - ", "*", " / "}).Draw(rt, "op")
		return genExpr(rt, depth-1) + op + genExpr(rt, depth-1)
	}
}

// Property (PR-01): случайное выражение из корректной грамматики разбирается и вычисляется без паники;
// единственная допустимая ошибка вычисления — деление на ноль.
func TestPR01_PropertyFormulaParsesAndEvaluatesWithoutPanic(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		src := genExpr(rt, rapid.IntRange(0, 6).Draw(rt, "depth"))
		if len(src) > pr.MaxFormulaLength {
			rt.Skip("слишком длинное")
		}
		f, err := pr.ParseFormula(src)
		if err != nil {
			rt.Fatalf("разбор %q: %v", src, err)
		}
		vars := map[string]decimal.Decimal{}
		for _, v := range varNames {
			vars[v] = decimal.NewFromInt(int64(rapid.IntRange(-5, 5).Draw(rt, "val_"+v)))
		}
		if _, err := f.Eval(vars); err != nil && !errors.Is(err, kernel.ErrValidation) {
			rt.Fatalf("вычисление %q: неожиданная ошибка %v", src, err)
		}
		// Повторный разбор исходного текста детерминирован.
		if again, err := pr.ParseFormula(f.Source()); err != nil || len(again.Variables()) != len(f.Variables()) {
			rt.Fatalf("повторный разбор %q: %v", src, err)
		}
	})
}
