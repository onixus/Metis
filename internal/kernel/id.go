// Package kernel содержит общие типы платформы: идентификаторы, деньги, даты,
// ошибки и доменные события. Пакет не зависит ни от одного другого модуля.
package kernel

import (
	"fmt"

	"github.com/google/uuid"
)

// ID — идентификатор сущности (UUID v7, монотонный по времени).
type ID = uuid.UUID

// NewID возвращает новый идентификатор.
func NewID() ID {
	id, err := uuid.NewV7()
	if err != nil {
		// uuid.NewV7 возвращает ошибку только при отказе источника случайности;
		// падение на v4 сохраняет уникальность.
		return uuid.New()
	}
	return id
}

// ParseID разбирает строковое представление идентификатора.
func ParseID(s string) (ID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: идентификатор %q: %w", ErrValidation, s, err)
	}
	return id, nil
}

// NilID — пустой идентификатор.
var NilID = uuid.Nil
