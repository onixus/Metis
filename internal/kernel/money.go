package kernel

import (
	"fmt"

	"github.com/shopspring/decimal"
)

// Money — денежная сумма в минорных единицах валюты (инвариант 6).
type Money struct {
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

// RUB — рубли в копейках.
func RUB(kopecks int64) Money { return Money{Amount: kopecks, Currency: "RUB"} }

// IsZero сообщает, пуста ли сумма.
func (m Money) IsZero() bool { return m.Amount == 0 }

// Add складывает суммы одной валюты.
func (m Money) Add(o Money) (Money, error) {
	if m.Currency == "" {
		return o, nil
	}
	if o.Currency == "" {
		return m, nil
	}
	if m.Currency != o.Currency {
		return Money{}, fmt.Errorf("%w: валюты %s и %s не совпадают", ErrValidation, m.Currency, o.Currency)
	}
	return Money{Amount: m.Amount + o.Amount, Currency: m.Currency}, nil
}

// MulCoef умножает сумму на коэффициент с округлением до минорной единицы (банковское округление).
func (m Money) MulCoef(k decimal.Decimal) Money {
	v := decimal.NewFromInt(m.Amount).Mul(k).RoundBank(0)
	return Money{Amount: v.IntPart(), Currency: m.Currency}
}

func (m Money) String() string {
	return fmt.Sprintf("%d.%02d %s", m.Amount/100, abs64(m.Amount%100), m.Currency)
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
