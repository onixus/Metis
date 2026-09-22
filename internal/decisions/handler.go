package decisions

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

// PublishPageHandler обрабатывает decisions.page.requested из outbox: создаёт страницу ADR из шаблона
// через ports.KnowledgeBase с метками metis/adr и page properties со связями, сохраняет PageID
// и публикует decisions.page.created. Единственное место записи в базу знаний (инвариант 5).
// Идемпотентен по Event.ID и по PageID решения: повторная доставка не создаёт вторую страницу.
// TODO(question-21): статус решения на странице после принятия/замены не синхронизируется.
type PublishPageHandler struct {
	svc *Service
	kb  ports.KnowledgeBase
	sc  authz.Scope
}

// NewPublishPageHandler создаёт обработчик, работающий от сервисного Scope.
func NewPublishPageHandler(svc *Service, kb ports.KnowledgeBase, sc authz.Scope) *PublishPageHandler {
	return &PublishPageHandler{svc: svc, kb: kb, sc: sc}
}

var _ kernel.Handler = (*PublishPageHandler)(nil)

// PageCreated — полезная нагрузка события decisions.page.created.
type PageCreated struct {
	DecisionID kernel.ID `json:"decision_id"`
	PageID     string    `json:"page_id"`
	URL        string    `json:"url,omitempty"`
}

// Handle обрабатывает событие; события других типов пропускает.
func (h *PublishPageHandler) Handle(ctx context.Context, ev kernel.Event) error {
	if ev.Type != EventPageRequested {
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
	var p PageRequest
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		return kernel.Invalid("payload", err.Error())
	}
	if p.DecisionID == kernel.NilID || p.SpaceKey == "" {
		return kernel.Invalid("payload", "decision_id и space_key обязательны")
	}
	rec, err := h.svc.store.Get(ctx, p.DecisionID)
	if err != nil {
		return err
	}
	if err := canWrite(h.sc, rec.ProductID); err != nil {
		return err
	}
	var pageURL string
	if rec.PageID == "" {
		page, err := h.kb.CreatePage(ctx, ports.CreatePageInput{
			IdempotencyKey: rec.ID.String(),
			SpaceKey:       p.SpaceKey,
			ParentID:       p.ParentID,
			Title:          adrTitlePrefix + rec.Title,
			Body:           RenderADR(rec),
			Labels:         []string{LabelMetis, LabelADR},
			Properties:     PageProperties(rec),
		})
		if err != nil {
			// Memory storage can retain this ID. PostgreSQL rolls the failed
			// handler back; the adapter must recover by the same stable key.
			// TODO(question-29): remote rename/delete or delayed visibility still
			// require reconciliation; the remote API has no idempotency guarantee.
			if page.ID != "" {
				rec.PageID = page.ID
				rec.UpdatedAt = h.svc.clock.Now()
				if serr := h.svc.store.Save(ctx, rec); serr != nil {
					return fmt.Errorf("create page: %w (страница %s не сохранена: %w)", err, page.ID, serr)
				}
			}
			return fmt.Errorf("create page: %w", err)
		}
		if page.ID == "" {
			return fmt.Errorf("%w: knowledge base returned an empty page id", kernel.ErrUnavailable)
		}
		pageURL = page.URL
		rec.PageID = page.ID
		rec.UpdatedAt = h.svc.clock.Now()
		if err := h.svc.store.Save(ctx, rec); err != nil {
			return fmt.Errorf("save decision: %w", err)
		}
	}
	// A prior attempt may have stored the page ID before decoration failed.
	// Do not ACK until both repeat-safe decorations and the completion event succeed.
	if err := h.kb.AddLabels(ctx, rec.PageID, []string{LabelMetis, LabelADR}); err != nil {
		return fmt.Errorf("page labels: %w", err)
	}
	if err := h.kb.SetProperties(ctx, rec.PageID, PageProperties(rec)); err != nil {
		return fmt.Errorf("page properties: %w", err)
	}
	if err := h.svc.emit(ctx, EventPageCreated, rec, h.sc.Subject(), PageCreated{DecisionID: rec.ID, PageID: rec.PageID, URL: pageURL}); err != nil {
		return err
	}
	if err := h.svc.store.MarkEventProcessed(ctx, ev.ID); err != nil {
		return fmt.Errorf("mark event processed: %w", err)
	}
	return nil
}
