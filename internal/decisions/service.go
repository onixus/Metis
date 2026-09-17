package decisions

import (
	"context"
	"fmt"
	"strings"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// Service — публичный интерфейс модуля решений.
type Service struct {
	store Store
	pub   kernel.Publisher
	clock kernel.Clock
	// metrics — источник фактических значений показателей для ревизии решения (DA-06).
	metrics MetricSource
}

// NewService создаёт сервис.
func NewService(store Store, pub kernel.Publisher, clock kernel.Clock) *Service {
	return &Service{store: store, pub: pub, clock: clock}
}

// Input — данные решения при создании и обновлении (DA-01, DA-06).
type Input struct {
	ProductID      kernel.ID // NilID — портфельное решение
	Title          string
	Context        string
	Snapshot       map[string]any
	Options        []Option
	ChosenKey      string
	Rationale      string
	ExpectedEffect string
	// Effect — измеримая часть эффекта: показатель экономики, целевое значение, период (DA-06).
	Effect     MeasurableEffect
	ReviewDate kernel.Date
	Links      []Link
}

func (in Input) validate() error {
	if strings.TrimSpace(in.Title) == "" {
		return kernel.Invalid("title", "обязателен")
	}
	if strings.TrimSpace(in.Context) == "" {
		return kernel.Invalid("context", "обязателен")
	}
	keys := map[string]struct{}{}
	for i, o := range in.Options {
		if strings.TrimSpace(o.Key) == "" || strings.TrimSpace(o.Title) == "" {
			return kernel.Invalid("options", fmt.Sprintf("вариант %d: ключ и название обязательны", i+1))
		}
		if _, dup := keys[o.Key]; dup {
			return kernel.Invalid("options", fmt.Sprintf("повтор ключа %q", o.Key))
		}
		keys[o.Key] = struct{}{}
	}
	if in.ChosenKey != "" {
		if _, ok := keys[in.ChosenKey]; !ok {
			return kernel.Invalid("chosen_key", fmt.Sprintf("вариант %q не входит в список", in.ChosenKey))
		}
	}
	for _, l := range in.Links {
		if !ValidLinkKind(l.Kind) {
			return kernel.Invalid("links", fmt.Sprintf("неизвестный вид связи %q", l.Kind))
		}
		if l.ID == kernel.NilID {
			return kernel.Invalid("links", "идентификатор связи обязателен")
		}
	}
	return nil
}

// canRead — чтение решения: стратегический срез продукта; портфельные решения — роли cpo/admin (и service).
// TODO(question-20): роли для портфельных решений в ТЗ не заданы.
func canRead(sc authz.Scope, productID kernel.ID) error {
	if !sc.Valid() {
		return kernel.ErrForbidden
	}
	if productID == kernel.NilID {
		if sc.HasRole(authz.RoleCPO) || sc.HasRole(authz.RoleAdmin) || sc.HasRole(authz.RoleService) {
			return nil
		}
		return kernel.ErrForbidden
	}
	return sc.Require(authz.ActionReadStrategic, productID)
}

// canWrite — запись решения: ActionWriteDecisions по продукту (для портфельного — по kernel.NilID).
func canWrite(sc authz.Scope, productID kernel.ID) error {
	return sc.Require(authz.ActionWriteDecisions, productID)
}

func (s *Service) emit(ctx context.Context, typ string, rec DecisionRecord, actor string, payload any) error {
	ev, err := kernel.NewEvent(s.clock, typ, rec.ID, rec.ProductID, actor, payload)
	if err != nil {
		return err
	}
	if err := s.pub.Publish(ctx, ev); err != nil {
		return fmt.Errorf("publish %s: %w", typ, err)
	}
	return nil
}

func (s *Service) save(ctx context.Context, sc authz.Scope, rec DecisionRecord) error {
	if err := s.store.Save(ctx, rec); err != nil {
		return fmt.Errorf("save decision: %w", err)
	}
	return s.emit(ctx, EventRecordSaved, rec, sc.Subject(), rec)
}

func apply(rec *DecisionRecord, in Input) {
	rec.Title = strings.TrimSpace(in.Title)
	rec.Context = strings.TrimSpace(in.Context)
	rec.Snapshot = copySnapshot(in.Snapshot)
	rec.Options = append([]Option(nil), in.Options...)
	rec.ChosenKey = in.ChosenKey
	rec.Rationale = strings.TrimSpace(in.Rationale)
	rec.ExpectedEffect = strings.TrimSpace(in.ExpectedEffect)
	rec.Effect = MeasurableEffect{MetricKey: strings.TrimSpace(in.Effect.MetricKey),
		Value: in.Effect.Value, Period: strings.TrimSpace(in.Effect.Period)}
	rec.ReviewDate = in.ReviewDate
	rec.Links = append([]Link(nil), in.Links...)
}

func copySnapshot(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Create создаёт решение в статусе proposed (DA-01). Право: ActionWriteDecisions по продукту.
func (s *Service) Create(ctx context.Context, sc authz.Scope, in Input) (DecisionRecord, error) {
	if err := in.validate(); err != nil {
		return DecisionRecord{}, err
	}
	if err := canWrite(sc, in.ProductID); err != nil {
		return DecisionRecord{}, err
	}
	now := s.clock.Now()
	rec := DecisionRecord{
		ID:        kernel.NewID(),
		ProductID: in.ProductID,
		Status:    StatusProposed,
		Author:    sc.Subject(),
		CreatedAt: now,
		UpdatedAt: now,
	}
	apply(&rec, in)
	if err := s.save(ctx, sc, rec); err != nil {
		return DecisionRecord{}, err
	}
	return rec, nil
}

// Update изменяет решение; допускается только в статусе proposed. Продукт решения не меняется.
func (s *Service) Update(ctx context.Context, sc authz.Scope, id kernel.ID, in Input) (DecisionRecord, error) {
	rec, err := s.store.Get(ctx, id)
	if err != nil {
		return DecisionRecord{}, err
	}
	if err := canWrite(sc, rec.ProductID); err != nil {
		return DecisionRecord{}, err
	}
	in.ProductID = rec.ProductID
	if err := in.validate(); err != nil {
		return DecisionRecord{}, err
	}
	if rec.Status != StatusProposed {
		return DecisionRecord{}, fmt.Errorf("%w: решение в статусе %s не редактируется", kernel.ErrConflict, rec.Status)
	}
	apply(&rec, in)
	rec.UpdatedAt = s.clock.Now()
	if err := s.save(ctx, sc, rec); err != nil {
		return DecisionRecord{}, err
	}
	return rec, nil
}

// Accept принимает решение: требуется выбранный вариант; из proposed в accepted.
func (s *Service) Accept(ctx context.Context, sc authz.Scope, id kernel.ID) (DecisionRecord, error) {
	rec, err := s.store.Get(ctx, id)
	if err != nil {
		return DecisionRecord{}, err
	}
	if err := canWrite(sc, rec.ProductID); err != nil {
		return DecisionRecord{}, err
	}
	if rec.Status != StatusProposed {
		return DecisionRecord{}, fmt.Errorf("%w: принять можно только предложенное решение, статус %s", kernel.ErrConflict, rec.Status)
	}
	if rec.ChosenKey == "" {
		return DecisionRecord{}, kernel.Invalid("chosen_key", "выбранный вариант обязателен для принятия")
	}
	rec.Status = StatusAccepted
	rec.UpdatedAt = s.clock.Now()
	if err := s.save(ctx, sc, rec); err != nil {
		return DecisionRecord{}, err
	}
	if err := s.emit(ctx, EventRecordAccepted, rec, sc.Subject(), rec); err != nil {
		return DecisionRecord{}, err
	}
	return rec, nil
}

// Reject отклоняет предложенное решение.
func (s *Service) Reject(ctx context.Context, sc authz.Scope, id kernel.ID) (DecisionRecord, error) {
	rec, err := s.store.Get(ctx, id)
	if err != nil {
		return DecisionRecord{}, err
	}
	if err := canWrite(sc, rec.ProductID); err != nil {
		return DecisionRecord{}, err
	}
	if rec.Status != StatusProposed {
		return DecisionRecord{}, fmt.Errorf("%w: отклонить можно только предложенное решение, статус %s", kernel.ErrConflict, rec.Status)
	}
	rec.Status = StatusRejected
	rec.UpdatedAt = s.clock.Now()
	if err := s.save(ctx, sc, rec); err != nil {
		return DecisionRecord{}, err
	}
	return rec, nil
}

// Supersede заменяет принятое решение другим (accepted → superseded, SupersededBy = by).
// Заменяющее решение должно быть принято и относиться к тому же продукту.
func (s *Service) Supersede(ctx context.Context, sc authz.Scope, id, by kernel.ID) (DecisionRecord, error) {
	if id == by {
		return DecisionRecord{}, kernel.Invalid("superseded_by", "решение не может заменять само себя")
	}
	rec, err := s.store.Get(ctx, id)
	if err != nil {
		return DecisionRecord{}, err
	}
	if err := canWrite(sc, rec.ProductID); err != nil {
		return DecisionRecord{}, err
	}
	repl, err := s.store.Get(ctx, by)
	if err != nil {
		return DecisionRecord{}, err
	}
	if repl.ProductID != rec.ProductID {
		return DecisionRecord{}, kernel.Invalid("superseded_by", "заменяющее решение относится к другому продукту")
	}
	if repl.Status != StatusAccepted {
		return DecisionRecord{}, fmt.Errorf("%w: заменяющее решение не принято", kernel.ErrConflict)
	}
	if rec.Status != StatusAccepted {
		return DecisionRecord{}, fmt.Errorf("%w: заменить можно только принятое решение, статус %s", kernel.ErrConflict, rec.Status)
	}
	rec.Status = StatusSuperseded
	rec.SupersededBy = by
	rec.UpdatedAt = s.clock.Now()
	if err := s.save(ctx, sc, rec); err != nil {
		return DecisionRecord{}, err
	}
	return rec, nil
}

// Get возвращает решение. Право: стратегический срез продукта.
func (s *Service) Get(ctx context.Context, sc authz.Scope, id kernel.ID) (DecisionRecord, error) {
	rec, err := s.store.Get(ctx, id)
	if err != nil {
		return DecisionRecord{}, err
	}
	if err := canRead(sc, rec.ProductID); err != nil {
		return DecisionRecord{}, err
	}
	return rec, nil
}

// List возвращает решения продукта (NilID — портфельные) с необязательным фильтром по статусу.
func (s *Service) List(ctx context.Context, sc authz.Scope, productID kernel.ID, status Status) ([]DecisionRecord, error) {
	if status != "" && !ValidStatus(status) {
		return nil, kernel.Invalid("status", fmt.Sprintf("неизвестный статус %q", status))
	}
	if err := canRead(sc, productID); err != nil {
		return nil, err
	}
	return s.store.List(ctx, Filter{ProductID: productID, HasProduct: true, Status: status})
}

// DecisionsFor возвращает решения, связанные с сущностью (DS-04; порт discovery.DecisionLinks).
// Решения продуктов, недоступных субъекту, опускаются.
func (s *Service) DecisionsFor(ctx context.Context, sc authz.Scope, kind string, id kernel.ID) ([]DecisionRef, error) {
	if !ValidLinkKind(LinkKind(kind)) {
		return nil, kernel.Invalid("kind", fmt.Sprintf("неизвестный вид связи %q", kind))
	}
	if id == kernel.NilID {
		return nil, kernel.Invalid("id", "обязателен")
	}
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	recs, err := s.store.List(ctx, Filter{Link: &Link{Kind: LinkKind(kind), ID: id}})
	if err != nil {
		return nil, fmt.Errorf("list decisions: %w", err)
	}
	out := make([]DecisionRef, 0, len(recs))
	for _, r := range recs {
		if canRead(sc, r.ProductID) != nil {
			continue
		}
		out = append(out, DecisionRef{ID: r.ID, Title: r.Title})
	}
	return out, nil
}

// PageRequest — полезная нагрузка события decisions.page.requested.
type PageRequest struct {
	DecisionID kernel.ID `json:"decision_id"`
	SpaceKey   string    `json:"space_key"`
	ParentID   string    `json:"parent_id,omitempty"`
}

// RequestPage запрашивает создание страницы ADR в базе знаний: публикует decisions.page.requested,
// страницу создаёт PublishPageHandler из outbox (инвариант 5). Повторный запрос для решения
// со страницей — ErrConflict.
func (s *Service) RequestPage(ctx context.Context, sc authz.Scope, id kernel.ID, spaceKey string) error {
	if strings.TrimSpace(spaceKey) == "" {
		return kernel.Invalid("space_key", "обязателен")
	}
	rec, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := canWrite(sc, rec.ProductID); err != nil {
		return err
	}
	if rec.PageID != "" {
		return fmt.Errorf("%w: страница решения уже создана: %s", kernel.ErrConflict, rec.PageID)
	}
	return s.emit(ctx, EventPageRequested, rec, sc.Subject(), PageRequest{DecisionID: rec.ID, SpaceKey: spaceKey})
}
