package pgdb

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onixus/metis/internal/kernel"
)

// Преобразования доменных типов kernel в типы pgx для sqlc-кода хранилищ.
// Пустые значения (NilID, нулевая дата, нулевое время) хранятся как NULL.

// NullID переводит идентификатор в nullable-UUID: NilID → NULL.
func NullID(id kernel.ID) uuid.NullUUID {
	return uuid.NullUUID{UUID: id, Valid: id != kernel.NilID}
}

// IDs возвращает непустой срез идентификаторов (nil → пустой массив PostgreSQL).
func IDs(ids []kernel.ID) []uuid.UUID {
	if ids == nil {
		return []uuid.UUID{}
	}
	return ids
}

// Strings возвращает непустой срез строк (nil → пустой массив PostgreSQL).
func Strings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// NilIfEmptyIDs возвращает nil для пустого среза: массив '{}' из БД читается как пустое доменное поле.
func NilIfEmptyIDs(ids []kernel.ID) []kernel.ID {
	if len(ids) == 0 {
		return nil
	}
	return ids
}

// NilIfEmptyStrings возвращает nil для пустого среза строк.
func NilIfEmptyStrings(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

// ToDate переводит kernel.Date в date; нулевая дата → NULL (инвариант 7: без времени).
func ToDate(d kernel.Date) pgtype.Date {
	if d.IsZero() {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: d.Time(), Valid: true}
}

// FromDate читает date; NULL → нулевая дата.
func FromDate(d pgtype.Date) kernel.Date {
	if !d.Valid {
		return kernel.Date{}
	}
	return kernel.DateFromTime(time.Date(d.Time.Year(), d.Time.Month(), d.Time.Day(), 0, 0, 0, 0, time.UTC))
}

// ToTime переводит время в nullable timestamptz в UTC; нулевое время → NULL.
func ToTime(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

// FromTime читает nullable timestamptz в UTC; NULL → нулевое время.
func FromTime(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time.UTC()
}
