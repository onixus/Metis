package roadmap

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/portfoliograph"
)

// ShiftHandler обрабатывает portfoliograph.EventDateShifted (PG-08 → RM-03):
// записывает историю дат для элементов roadmap, привязанных к сдвинутой и затронутым фичам,
// и сдвигает дату окончания элементов источника на новую дату фичи.
// Идемпотентен по Event.ID: повторная доставка не дублирует историю.
type ShiftHandler struct {
	svc *Service
	sc  authz.Scope
}

// NewShiftHandler создаёт обработчик, работающий от сервисного Scope.
func NewShiftHandler(svc *Service, sc authz.Scope) *ShiftHandler {
	return &ShiftHandler{svc: svc, sc: sc}
}

var _ kernel.Handler = (*ShiftHandler)(nil)

type shiftPayload struct {
	portfoliograph.ShiftResult
	Reason string `json:"reason"`
}

// Handle обрабатывает событие; события других типов пропускает.
func (h *ShiftHandler) Handle(ctx context.Context, ev kernel.Event) error {
	if ev.Type != portfoliograph.EventDateShifted {
		return nil
	}
	if !h.sc.Valid() {
		return kernel.ErrForbidden
	}
	done, err := h.svc.store.EventProcessed(ctx, ev.ID)
	if err != nil {
		return fmt.Errorf("event processed: %w", err)
	}
	if done {
		return nil
	}
	var p shiftPayload
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		return kernel.Invalid("payload", err.Error())
	}
	if p.SourceFeature == kernel.NilID || p.NewDate.IsZero() {
		return kernel.Invalid("payload", "source_feature и new_date обязательны")
	}
	reason := p.Reason
	if reason == "" {
		reason = "сдвиг срока фичи " + p.SourceFeature.String()
	}

	// Источник: сдвиг даты окончания на новую дату фичи.
	src, err := h.svc.store.ItemsByFeature(ctx, p.SourceFeature)
	if err != nil {
		return fmt.Errorf("items by feature: %w", err)
	}
	for _, it := range src {
		start := it.StartDate
		if !start.IsZero() && p.NewDate.Before(start) {
			start = p.NewDate
		}
		if _, err := h.svc.changeDates(ctx, h.sc, it.ID, start, p.NewDate, reason, ev.ID); err != nil {
			return fmt.Errorf("shift item %s: %w", it.ID, err)
		}
	}

	// TODO(question-06): затронутые фичи — запись в историю без изменения дат; новую дату подтверждает владелец продукта.
	affectedReason := fmt.Sprintf("затронуто сдвигом фичи %s: %s", p.SourceFeature, reason)
	for _, a := range p.Affected {
		items, err := h.svc.store.ItemsByFeature(ctx, a.FeatureID)
		if err != nil {
			return fmt.Errorf("items by feature: %w", err)
		}
		for _, it := range items {
			if _, err := h.svc.changeDates(ctx, h.sc, it.ID, it.StartDate, it.EndDate, affectedReason, ev.ID); err != nil {
				return fmt.Errorf("mark affected item %s: %w", it.ID, err)
			}
		}
	}
	if err := h.svc.store.MarkEventProcessed(ctx, ev.ID); err != nil {
		return fmt.Errorf("mark event processed: %w", err)
	}
	return nil
}
