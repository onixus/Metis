package kernel

import (
	"errors"
	"fmt"
)

// Базовые классы ошибок домена. Проверяются через errors.Is.
var (
	ErrNotFound    = errors.New("не найдено")
	ErrForbidden   = errors.New("доступ запрещён")
	ErrConflict    = errors.New("конфликт состояния")
	ErrValidation  = errors.New("некорректные данные")
	ErrUnavailable = errors.New("внешняя система недоступна")
)

// ValidationError — ошибка валидации с полем и деталями.
type ValidationError struct {
	Field   string
	Message string
	Details map[string]any
}

func (e *ValidationError) Error() string {
	if e.Field == "" {
		return fmt.Sprintf("%v: %s", ErrValidation, e.Message)
	}
	return fmt.Sprintf("%v: %s: %s", ErrValidation, e.Field, e.Message)
}

// Is позволяет errors.Is(err, ErrValidation).
func (e *ValidationError) Is(target error) bool { return target == ErrValidation }

// Invalid создаёт ошибку валидации поля.
func Invalid(field, message string) error {
	return &ValidationError{Field: field, Message: message}
}

// NotFound создаёт ошибку «не найдено» для сущности.
func NotFound(entity string, id ID) error {
	return fmt.Errorf("%w: %s %s", ErrNotFound, entity, id)
}

// IsNotFound — сокращение для errors.Is(err, ErrNotFound).
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }
