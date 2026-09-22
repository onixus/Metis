// Package formula — движок расчётных показателей экономики (EC-09, EC-10).
// Выражения компилируются cel-go в среде без макросов, ввода-вывода и рефлексии:
// произвольный код в формуле невозможен. Арифметика ведётся в decimal, а не в float,
// поэтому денежные значения не теряют точность (инвариант 6, ADR-0005).
package formula

import (
	"fmt"
	"math"
	"reflect"

	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/common/types/traits"
	"github.com/shopspring/decimal"
)

// DecimalType — тип значения показателя в среде CEL. Маска traits говорит стандартной
// библиотеке CEL, что значение участвует в арифметике и сравнениях.
var DecimalType = types.NewObjectType("metis.Decimal",
	traits.AdderType|traits.SubtractorType|traits.MultiplierType|
		traits.DividerType|traits.NegatorType|traits.ComparerType)

// decVal — значение decimal внутри CEL.
type decVal struct{ d decimal.Decimal }

// Dec оборачивает decimal в значение CEL.
func Dec(d decimal.Decimal) ref.Val { return decVal{d: d} }

var (
	_ ref.Val           = decVal{}
	_ traits.Adder      = decVal{}
	_ traits.Subtractor = decVal{}
	_ traits.Multiplier = decVal{}
	_ traits.Divider    = decVal{}
	_ traits.Negater    = decVal{}
	_ traits.Comparer   = decVal{}
)

// Add складывает значения.
func (v decVal) Add(other ref.Val) ref.Val { return apply(v, other, decimal.Decimal.Add) }

// Subtract вычитает значение.
func (v decVal) Subtract(other ref.Val) ref.Val { return apply(v, other, decimal.Decimal.Sub) }

// Multiply умножает значения.
func (v decVal) Multiply(other ref.Val) ref.Val { return apply(v, other, decimal.Decimal.Mul) }

// Divide делит значения; деление на ноль — ошибка, а не паника (инвариант 9).
func (v decVal) Divide(other ref.Val) ref.Val {
	o, err := toDecimal(other)
	if err != nil {
		return types.NewErr("%v", err)
	}
	if o.IsZero() {
		return types.NewErr("деление на ноль")
	}
	return Dec(v.d.DivRound(o, divisionScale))
}

// Negate меняет знак.
func (v decVal) Negate() ref.Val { return Dec(v.d.Neg()) }

// Compare возвращает -1, 0 или 1.
func (v decVal) Compare(other ref.Val) ref.Val {
	o, err := toDecimal(other)
	if err != nil {
		return types.NewErr("%v", err)
	}
	return types.Int(v.d.Cmp(o))
}

// divisionScale — число знаков после запятой при делении.
const divisionScale = 10

func apply(v decVal, other ref.Val, f func(a, b decimal.Decimal) decimal.Decimal) ref.Val {
	o, err := toDecimal(other)
	if err != nil {
		return types.NewErr("%v", err)
	}
	return Dec(f(v.d, o))
}

// ConvertToNative отдаёт значение в виде decimal, строки или числа с плавающей точкой.
func (v decVal) ConvertToNative(typeDesc reflect.Type) (any, error) {
	switch typeDesc {
	case reflect.TypeOf(decimal.Decimal{}):
		return v.d, nil
	case reflect.TypeOf(""):
		return v.d.String(), nil
	case reflect.TypeOf(float64(0)):
		f, _ := v.d.Float64()
		return f, nil
	case reflect.TypeOf(int64(0)):
		return v.d.IntPart(), nil
	}
	return nil, fmt.Errorf("decimal не приводится к %v", typeDesc)
}

// ConvertToType приводит значение к типу CEL.
func (v decVal) ConvertToType(t ref.Type) ref.Val {
	switch t {
	case DecimalType:
		return v
	case types.StringType:
		return types.String(v.d.String())
	case types.DoubleType:
		f, _ := v.d.Float64()
		return types.Double(f)
	case types.IntType:
		return types.Int(v.d.IntPart())
	case types.TypeType:
		return DecimalType
	}
	return types.NewErr("decimal не приводится к %v", t)
}

// Equal сравнивает значения по числовому равенству.
func (v decVal) Equal(other ref.Val) ref.Val {
	o, err := toDecimal(other)
	if err != nil {
		return types.NewErr("%v", err)
	}
	return types.Bool(v.d.Equal(o))
}

// Type возвращает тип значения.
func (v decVal) Type() ref.Type { return DecimalType }

// Value возвращает decimal.
func (v decVal) Value() any { return v.d }

// toDecimal приводит значение CEL к decimal; целые и вещественные литералы допускаются,
// чтобы в формуле можно было писать коэффициенты.
func toDecimal(v ref.Val) (decimal.Decimal, error) {
	switch t := v.(type) {
	case decVal:
		return t.d, nil
	case types.Int:
		return decimal.NewFromInt(int64(t)), nil
	case types.Uint:
		return decimal.NewFromUint64(uint64(t)), nil
	case types.Double:
		f := float64(t)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return decimal.Zero, fmt.Errorf("недопустимое число %v", f)
		}
		return decimal.NewFromFloat(f), nil
	case types.String:
		d, err := decimal.NewFromString(string(t))
		if err != nil {
			return decimal.Zero, fmt.Errorf("строка %q не число: %w", string(t), err)
		}
		return d, nil
	}
	return decimal.Zero, fmt.Errorf("значение типа %v не число", v.Type())
}
