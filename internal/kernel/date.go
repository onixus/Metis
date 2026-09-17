package kernel

import (
	"encoding/json"
	"fmt"
	"time"
)

// Date — календарная дата без времени (инвариант 7). Используется для плановых дат.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// DateOf строит дату из компонентов.
func DateOf(y int, m time.Month, d int) Date { return Date{Year: y, Month: m, Day: d} }

// DateFromTime берёт дату из момента времени в UTC.
func DateFromTime(t time.Time) Date {
	y, m, d := t.UTC().Date()
	return Date{Year: y, Month: m, Day: d}
}

// ParseDate разбирает дату в формате YYYY-MM-DD.
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return Date{}, fmt.Errorf("%w: дата %q: %w", ErrValidation, s, err)
	}
	return DateFromTime(t), nil
}

// IsZero сообщает, задана ли дата.
func (d Date) IsZero() bool { return d.Year == 0 && d.Month == 0 && d.Day == 0 }

// Time возвращает полночь UTC этой даты.
func (d Date) Time() time.Time { return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC) }

// AddDays сдвигает дату на n дней (n может быть отрицательным).
func (d Date) AddDays(n int) Date { return DateFromTime(d.Time().AddDate(0, 0, n)) }

// DaysUntil возвращает число дней от d до o (отрицательное, если o раньше).
func (d Date) DaysUntil(o Date) int { return int(o.Time().Sub(d.Time()).Hours() / 24) }

// Before сообщает, раньше ли d, чем o.
func (d Date) Before(o Date) bool { return d.Time().Before(o.Time()) }

// After сообщает, позже ли d, чем o.
func (d Date) After(o Date) bool { return d.Time().After(o.Time()) }

// Compare возвращает -1, 0 или 1.
func (d Date) Compare(o Date) int { return d.Time().Compare(o.Time()) }

// MaxDate возвращает позднюю из дат; пустые даты игнорируются.
func MaxDate(dates ...Date) Date {
	var out Date
	for _, d := range dates {
		if d.IsZero() {
			continue
		}
		if out.IsZero() || d.After(out) {
			out = d
		}
	}
	return out
}

func (d Date) String() string {
	if d.IsZero() {
		return ""
	}
	return d.Time().Format(time.DateOnly)
}

// MarshalJSON выдаёт YYYY-MM-DD или null.
func (d Date) MarshalJSON() ([]byte, error) {
	if d.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(d.String())
}

// UnmarshalJSON читает YYYY-MM-DD или null.
func (d *Date) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*d = Date{}
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("%w: дата: %w", ErrValidation, err)
	}
	v, err := ParseDate(s)
	if err != nil {
		return err
	}
	*d = v
	return nil
}
