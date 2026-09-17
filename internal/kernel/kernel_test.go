package kernel_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/kernel"
)

func TestMoney_AddAndMul(t *testing.T) {
	a := kernel.RUB(12_000_000_00)
	b := kernel.RUB(50)
	s, err := a.Add(b)
	if err != nil || s.Amount != 12_000_000_50 {
		t.Fatalf("add: %v %v", s, err)
	}
	if _, err := a.Add(kernel.Money{Amount: 1, Currency: "USD"}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("ожидалась ошибка валют, получено %v", err)
	}
	half := a.MulCoef(decimal.NewFromFloat(0.5))
	if half.Amount != 6_000_000_00 {
		t.Fatalf("mul: %v", half)
	}
}

func TestDate_JSONAndArithmetic(t *testing.T) {
	d := kernel.DateOf(2026, time.September, 17)
	raw, _ := json.Marshal(d)
	if string(raw) != `"2026-09-17"` {
		t.Fatalf("json: %s", raw)
	}
	var back kernel.Date
	if err := json.Unmarshal(raw, &back); err != nil || back != d {
		t.Fatalf("unmarshal: %v %v", back, err)
	}
	if d.AddDays(21).String() != "2026-10-08" {
		t.Fatalf("add days: %s", d.AddDays(21))
	}
	if d.DaysUntil(d.AddDays(21)) != 21 {
		t.Fatal("days until")
	}
	if kernel.MaxDate(kernel.Date{}, d, d.AddDays(-1)) != d {
		t.Fatal("max date")
	}
}

func TestCanonicalJSON_SortsKeys(t *testing.T) {
	v := map[string]any{"b": 1, "a": []any{true, nil, "x<y"}, "c": map[string]any{"z": 1.5, "y": "ё"}}
	got, err := kernel.CanonicalJSON(v)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":[true,null,"x<y"],"b":1,"c":{"y":"ё","z":1.5}}`
	if string(got) != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestValidationError_Is(t *testing.T) {
	err := kernel.Invalid("name", "пусто")
	if !errors.Is(err, kernel.ErrValidation) {
		t.Fatal("errors.Is")
	}
	var ve *kernel.ValidationError
	if !errors.As(err, &ve) || ve.Field != "name" {
		t.Fatal("errors.As")
	}
}
