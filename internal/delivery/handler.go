package delivery

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

// CreateEpicHandler — обработчик outbox (воркер) для команды EventEpicCreateRequested (DL-01):
// единственное место, где платформа пишет в трекер. Идемпотентен по Event.ID и по ExternalKey фичи.
type CreateEpicHandler struct {
	tracker   ports.DeliveryTracker
	svc       *Service
	portfolio Portfolio
	sc        authz.Scope
}

var _ kernel.Handler = (*CreateEpicHandler)(nil)

// NewCreateEpicHandler создаёт обработчик. sc — сервисная область доступа воркера (создаётся identityaccess).
func NewCreateEpicHandler(tracker ports.DeliveryTracker, svc *Service, portfolio Portfolio, sc authz.Scope) *CreateEpicHandler {
	return &CreateEpicHandler{tracker: tracker, svc: svc, portfolio: portfolio, sc: sc}
}

// Handle создаёт эпик через порт и записывает ключ обратно в фичу.
func (h *CreateEpicHandler) Handle(ctx context.Context, ev kernel.Event) error {
	if ev.Type != EventEpicCreateRequested {
		return nil
	}
	fresh, err := h.svc.store.MarkProcessed(ctx, "event:"+ev.ID.String())
	if err != nil {
		return fmt.Errorf("mark processed: %w", err)
	}
	if !fresh {
		return nil
	}
	var req EpicCreateRequest
	if err := json.Unmarshal(ev.Payload, &req); err != nil {
		return fmt.Errorf("%w: payload %s: %w", kernel.ErrValidation, ev.Type, err)
	}
	f, err := h.portfolio.Feature(ctx, h.sc, req.FeatureID)
	if err != nil {
		return fmt.Errorf("feature %s: %w", req.FeatureID, err)
	}
	if f.ExternalKey != "" {
		return nil // эпик уже создан (повторная доставка после сбоя между записью и подтверждением)
	}
	summary := req.Summary
	if summary == "" {
		summary = f.Name
	}
	key, err := h.tracker.CreateEpic(ctx, req.Project, summary, req.Description, req.FeatureID.String())
	if err != nil {
		return fmt.Errorf("create epic: %w", err)
	}
	if _, err := h.svc.MapFeature(ctx, h.sc, req.FeatureID, key, req.Project); err != nil {
		return fmt.Errorf("map feature: %w", err)
	}
	return nil
}
