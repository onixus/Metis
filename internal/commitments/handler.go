package commitments

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/portfoliograph"
	"github.com/onixus/metis/internal/roadmap"
)

// ShiftHandler обрабатывает сдвиги дат (CT-03): portfoliograph.EventDateShifted (PG-08) и
// roadmap.EventDatesChanged (RM-03). Для каждого активного обязательства, привязанного к
// сдвинутой или затронутой фиче (для roadmap — к фиче или релизу элемента), у которого новая
// дата позже срока, поднимает Alert и событие commitments.alert.raised.
// Идемпотентен по Event.ID: повторная доставка не дублирует алерты.
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
	if ev.Type != portfoliograph.EventDateShifted && ev.Type != roadmap.EventDatesChanged {
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
	switch ev.Type {
	case portfoliograph.EventDateShifted:
		err = h.handleFeatureShift(ctx, ev)
	case roadmap.EventDatesChanged:
		err = h.handleRoadmapChange(ctx, ev)
	}
	if err != nil {
		return err
	}
	if err := h.svc.store.MarkEventProcessed(ctx, ev.ID); err != nil {
		return fmt.Errorf("mark event processed: %w", err)
	}
	return nil
}

// handleFeatureShift: источник сдвига проверяется по NewDate, затронутые фичи — по ImpliedDate.
func (h *ShiftHandler) handleFeatureShift(ctx context.Context, ev kernel.Event) error {
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
	if err := h.checkFeature(ctx, p.SourceFeature, p.NewDate, ev.ID, reason); err != nil {
		return err
	}
	for _, a := range p.Affected {
		if err := h.checkFeature(ctx, a.FeatureID, a.ImpliedDate, ev.ID, fmt.Sprintf("затронуто сдвигом фичи %s: %s", p.SourceFeature, reason)); err != nil {
			return err
		}
	}
	return nil
}

func (h *ShiftHandler) checkFeature(ctx context.Context, featureID kernel.ID, newDate kernel.Date, eventID kernel.ID, reason string) error {
	if featureID == kernel.NilID || newDate.IsZero() {
		return nil
	}
	list, err := h.svc.store.List(ctx, Filter{FeatureID: featureID, Statuses: []Status{StatusActive}})
	if err != nil {
		return fmt.Errorf("list commitments: %w", err)
	}
	for _, c := range list {
		if _, err := h.svc.raiseAlert(ctx, h.sc, c, newDate, eventID, reason); err != nil {
			return err
		}
	}
	return nil
}

// handleRoadmapChange: обязательства фичи и релиза элемента проверяются по новой дате окончания.
// Без порта RoadmapReader привязки элемента неизвестны — событие пропускается.
func (h *ShiftHandler) handleRoadmapChange(ctx context.Context, ev kernel.Event) error {
	if h.svc.reader == nil {
		return nil
	}
	var ch roadmap.DateChange
	if err := json.Unmarshal(ev.Payload, &ch); err != nil {
		return kernel.Invalid("payload", err.Error())
	}
	if ch.ItemID == kernel.NilID || ch.NewEnd.IsZero() {
		return kernel.Invalid("payload", "item_id и new_end обязательны")
	}
	if !ch.NewEnd.After(ch.OldEnd) {
		return nil
	}
	featureID, releaseID, err := h.svc.reader.ItemLinks(ctx, h.sc, ch.ItemID)
	if err != nil {
		return fmt.Errorf("item links: %w", err)
	}
	reason := "сдвиг элемента roadmap " + ch.ItemID.String()
	if ch.Reason != "" {
		reason += ": " + ch.Reason
	}
	seen := map[kernel.ID]bool{}
	for _, f := range []Filter{{FeatureID: featureID}, {ReleaseID: releaseID}} {
		if f.FeatureID == kernel.NilID && f.ReleaseID == kernel.NilID {
			continue
		}
		f.Statuses = []Status{StatusActive}
		list, err := h.svc.store.List(ctx, f)
		if err != nil {
			return fmt.Errorf("list commitments: %w", err)
		}
		for _, c := range list {
			if seen[c.ID] {
				continue
			}
			seen[c.ID] = true
			if _, err := h.svc.raiseAlert(ctx, h.sc, c, ch.NewEnd, ev.ID, reason); err != nil {
				return err
			}
		}
	}
	return nil
}
