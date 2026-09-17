package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// keyPattern — допустимый ключ кастомного поля и пользовательского статуса (AD-03).
var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// ValidKey сообщает, допустим ли ключ поля или статуса.
func ValidKey(k string) bool { return keyPattern.MatchString(k) }

func (d CustomFieldDef) validate() error {
	if !ValidEntity(d.Entity) {
		return kernel.Invalid("entity", fmt.Sprintf("неизвестная сущность %q", d.Entity))
	}
	if !ValidKey(d.Key) {
		return kernel.Invalid("key", "ожидается ^[a-z][a-z0-9_]{0,31}$")
	}
	if strings.TrimSpace(d.Label) == "" {
		return kernel.Invalid("label", "обязателен")
	}
	if !ValidFieldType(d.Type) {
		return kernel.Invalid("type", fmt.Sprintf("неизвестный тип %q", d.Type))
	}
	if d.Type == FieldEnum && len(d.Options) == 0 {
		return kernel.Invalid("options", "enum без вариантов")
	}
	if d.Type != FieldEnum && len(d.Options) > 0 {
		return kernel.Invalid("options", "варианты допустимы только для enum")
	}
	return nil
}

func (d CustomStatusDef) validate() error {
	if !ValidEntity(d.Entity) {
		return kernel.Invalid("entity", fmt.Sprintf("неизвестная сущность %q", d.Entity))
	}
	if !ValidKey(d.Key) {
		return kernel.Invalid("key", "ожидается ^[a-z][a-z0-9_]{0,31}$")
	}
	if strings.TrimSpace(d.Label) == "" {
		return kernel.Invalid("label", "обязателен")
	}
	switch d.Entity {
	case EntityHypothesis:
		if !BuiltinHypothesisStatus(HypothesisStatus(d.Category)) {
			return kernel.Invalid("category", "для гипотезы: draft|testing|confirmed|rejected")
		}
		if BuiltinHypothesisStatus(HypothesisStatus(d.Key)) {
			return kernel.Invalid("key", "совпадает со встроенным статусом")
		}
	default:
		// Категории feature и signal принадлежат их модулям; здесь только непустота.
		if strings.TrimSpace(d.Category) == "" {
			return kernel.Invalid("category", "обязательна")
		}
	}
	return nil
}

// DefineCustomField создаёт или обновляет определение кастомного поля (AD-03).
// Право: администрирование настроек.
func (s *Service) DefineCustomField(ctx context.Context, sc authz.Scope, def CustomFieldDef) (CustomFieldDef, error) {
	if err := sc.Require(authz.ActionAdminSettings, kernel.NilID); err != nil {
		return CustomFieldDef{}, err
	}
	if err := def.validate(); err != nil {
		return CustomFieldDef{}, err
	}
	existing, err := s.store.FieldDefs(ctx, def.Entity)
	if err != nil {
		return CustomFieldDef{}, fmt.Errorf("field defs: %w", err)
	}
	def.ID = kernel.NewID()
	for _, e := range existing {
		if e.Key == def.Key {
			def.ID = e.ID
		}
	}
	if err := s.store.SaveFieldDef(ctx, def); err != nil {
		return CustomFieldDef{}, fmt.Errorf("save field def: %w", err)
	}
	return def, nil
}

// CustomFields возвращает определения полей сущности. Доступно любому аутентифицированному субъекту.
func (s *Service) CustomFields(ctx context.Context, sc authz.Scope, entity Entity) ([]CustomFieldDef, error) {
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	if !ValidEntity(entity) {
		return nil, kernel.Invalid("entity", fmt.Sprintf("неизвестная сущность %q", entity))
	}
	defs, err := s.store.FieldDefs(ctx, entity)
	if err != nil {
		return nil, fmt.Errorf("field defs: %w", err)
	}
	return defs, nil
}

// ValidateCustomFields проверяет значения кастомных полей сущности по текущим определениям.
// Для hypothesis вызывается при сохранении; для feature и signal — их модулями (следующая волна).
func (s *Service) ValidateCustomFields(ctx context.Context, entity Entity, values map[string]any) error {
	if !ValidEntity(entity) {
		return kernel.Invalid("entity", fmt.Sprintf("неизвестная сущность %q", entity))
	}
	defs, err := s.store.FieldDefs(ctx, entity)
	if err != nil {
		return fmt.Errorf("field defs: %w", err)
	}
	return ValidateValues(defs, values)
}

// ValidateValues проверяет значения по определениям: неизвестные ключи запрещены, обязательные
// поля присутствуют и непусты, тип значения соответствует определению.
func ValidateValues(defs []CustomFieldDef, values map[string]any) error {
	byKey := make(map[string]CustomFieldDef, len(defs))
	for _, d := range defs {
		byKey[d.Key] = d
	}
	for k := range values {
		if _, ok := byKey[k]; !ok {
			return kernel.Invalid("custom_fields."+k, "поле не определено")
		}
	}
	for _, d := range defs {
		v, ok := values[d.Key]
		if !ok || v == nil {
			if d.Required {
				return kernel.Invalid("custom_fields."+d.Key, "обязательное поле")
			}
			continue
		}
		if err := checkValue(d, v); err != nil {
			return err
		}
	}
	return nil
}

func checkValue(d CustomFieldDef, v any) error {
	field := "custom_fields." + d.Key
	switch d.Type {
	case FieldString:
		str, ok := v.(string)
		if !ok {
			return kernel.Invalid(field, "ожидается строка")
		}
		if d.Required && strings.TrimSpace(str) == "" {
			return kernel.Invalid(field, "обязательное поле")
		}
	case FieldNumber:
		switch n := v.(type) {
		case int, int32, int64, float32, float64:
		case json.Number:
			if _, err := n.Float64(); err != nil {
				return kernel.Invalid(field, "ожидается число")
			}
		default:
			return kernel.Invalid(field, "ожидается число")
		}
	case FieldDate:
		switch dt := v.(type) {
		case kernel.Date:
			if dt.IsZero() {
				return kernel.Invalid(field, "ожидается дата")
			}
		case string:
			if _, err := kernel.ParseDate(dt); err != nil {
				return kernel.Invalid(field, "ожидается дата YYYY-MM-DD")
			}
		default:
			return kernel.Invalid(field, "ожидается дата YYYY-MM-DD")
		}
	case FieldEnum:
		str, ok := v.(string)
		if !ok || !slices.Contains(d.Options, str) {
			return kernel.Invalid(field, fmt.Sprintf("ожидается одно из %v", d.Options))
		}
	}
	return nil
}

// DefineCustomStatus создаёт или обновляет пользовательский статус (AD-03).
// Право: администрирование настроек.
func (s *Service) DefineCustomStatus(ctx context.Context, sc authz.Scope, def CustomStatusDef) (CustomStatusDef, error) {
	if err := sc.Require(authz.ActionAdminSettings, kernel.NilID); err != nil {
		return CustomStatusDef{}, err
	}
	if err := def.validate(); err != nil {
		return CustomStatusDef{}, err
	}
	if err := s.store.SaveStatusDef(ctx, def); err != nil {
		return CustomStatusDef{}, fmt.Errorf("save status def: %w", err)
	}
	return def, nil
}

// CustomStatuses возвращает пользовательские статусы сущности.
func (s *Service) CustomStatuses(ctx context.Context, sc authz.Scope, entity Entity) ([]CustomStatusDef, error) {
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	if !ValidEntity(entity) {
		return nil, kernel.Invalid("entity", fmt.Sprintf("неизвестная сущность %q", entity))
	}
	defs, err := s.store.StatusDefs(ctx, entity)
	if err != nil {
		return nil, fmt.Errorf("status defs: %w", err)
	}
	return defs, nil
}

// hypothesisCategory отображает статус гипотезы на встроенную категорию: встроенный статус —
// сам себе категория, пользовательский — по определению; неизвестный — ошибка валидации.
func (s *Service) hypothesisCategory(ctx context.Context, st HypothesisStatus) (HypothesisStatus, error) {
	if BuiltinHypothesisStatus(st) {
		return st, nil
	}
	defs, err := s.store.StatusDefs(ctx, EntityHypothesis)
	if err != nil {
		return "", fmt.Errorf("status defs: %w", err)
	}
	for _, d := range defs {
		if HypothesisStatus(d.Key) == st {
			return HypothesisStatus(d.Category), nil
		}
	}
	return "", kernel.Invalid("status", fmt.Sprintf("неизвестный статус %q", st))
}
