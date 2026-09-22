package prioritization_test

import (
	"testing"

	"github.com/onixus/metis/internal/prioritization"
)

func TestEC09_NFS15_ConditionalFormulaIsLazyAndArityBounded(t *testing.T) {
	for source, want := range map[string]string{"ifgt(1,0,7,1/0)": "7", "ifge(0,0,8,1/0)": "8", "ifeq(0,1,1/0,9)": "9"} {
		formula, err := prioritization.ParseFormula(source)
		if err != nil {
			t.Fatal(err)
		}
		value, err := formula.Eval(nil)
		if err != nil || value.String() != want {
			t.Fatalf("%s = %s (%v)", source, value, err)
		}
	}
	for _, source := range []string{"ifgt(1,0,7)", "ifge(1,0,7,8,9)", "exec(1)", "while(1)"} {
		if _, err := prioritization.ParseFormula(source); err == nil {
			t.Fatalf("unsafe formula accepted: %s", source)
		}
	}
}
