package commitments

import (
	"context"
	"fmt"
	"strings"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/portfoliograph"
)

// RoadmapWriter — порт создания элемента roadmap на продление сертификата (CT-04).
// Реализует модуль roadmap. EnsureRenewalItem идемпотентен по commitmentID: повторный вызов
// возвращает идентификатор уже созданного элемента.
type RoadmapWriter interface {
	EnsureRenewalItem(ctx context.Context, sc authz.Scope, productID, commitmentID kernel.ID, title string, start, end kernel.Date) (kernel.ID, error)
}

// RoadmapReader — порт чтения привязок элемента roadmap (фича и релиз; NilID — нет привязки).
// Нужен обработчику события roadmap.EventDatesChanged: его payload (roadmap.DateChange) несёт
// только ItemID. Реализует модуль roadmap; без порта события roadmap пропускаются.
// Сигнатуры портов используют только kernel и authz: commitments импортирует roadmap
// (типы события), поэтому roadmap не может импортировать commitments.
// TODO(question-13): в roadmap нет публичного метода чтения элемента по идентификатору.
type RoadmapReader interface {
	ItemLinks(ctx context.Context, sc authz.Scope, itemID kernel.ID) (featureID, releaseID kernel.ID, err error)
}

// Service — публичный интерфейс модуля обязательств.
type Service struct {
	store  Store
	pub    kernel.Publisher
	clock  kernel.Clock
	writer RoadmapWriter
	reader RoadmapReader
}

// NewService создаёт сервис.
func NewService(store Store, pub kernel.Publisher, clock kernel.Clock) *Service {
	return &Service{store: store, pub: pub, clock: clock}
}

// WithRoadmapWriter подключает порт создания элементов roadmap (CT-04).
func (s *Service) WithRoadmapWriter(w RoadmapWriter) *Service {
	s.writer = w
	return s
}

// WithRoadmapReader подключает порт чтения привязок элементов roadmap (CT-03).
func (s *Service) WithRoadmapReader(r RoadmapReader) *Service {
	s.reader = r
	return s
}

var _ portfoliograph.CommitmentChecker = (*Service)(nil)

func (s *Service) emit(ctx context.Context, typ string, aggregate, product kernel.ID, actor string, payload any) error {
	ev, err := kernel.NewEvent(s.clock, typ, aggregate, product, actor, payload)
	if err != nil {
		return err
	}
	if err := s.pub.Publish(ctx, ev); err != nil {
		return fmt.Errorf("publish %s: %w", typ, err)
	}
	return nil
}

// Input — данные обязательства при создании и изменении (CT-01, CT-02).
type Input struct {
	Kind         Kind
	Subtype      Subtype
	Counterparty string
	Subject      string
	DueDate      kernel.Date
	Basis        string
	Owner        string
	FeatureID    kernel.ID
	ReleaseID    kernel.ID
}

func (in Input) validate() error {
	if !ValidKind(in.Kind) {
		return kernel.Invalid("kind", fmt.Sprintf("неизвестный вид %q", in.Kind))
	}
	switch in.Kind {
	case KindRegulatory:
		if !ValidSubtype(in.Subtype) {
			return kernel.Invalid("subtype", fmt.Sprintf("для регуляторного обязательства нужен подтип, получен %q", in.Subtype))
		}
	case KindCustomer:
		if in.Subtype != "" {
			return kernel.Invalid("subtype", "подтип задаётся только для регуляторных обязательств")
		}
	}
	if strings.TrimSpace(in.Counterparty) == "" {
		return kernel.Invalid("counterparty", "обязателен")
	}
	if strings.TrimSpace(in.Subject) == "" {
		return kernel.Invalid("subject", "обязателен")
	}
	if in.DueDate.IsZero() {
		return kernel.Invalid("due_date", "обязателен")
	}
	if strings.TrimSpace(in.Basis) == "" {
		return kernel.Invalid("basis", "обязательно")
	}
	if strings.TrimSpace(in.Owner) == "" {
		return kernel.Invalid("owner", "обязателен")
	}
	return nil
}

func (in Input) apply(c *Commitment) {
	c.Kind = in.Kind
	c.Subtype = in.Subtype
	c.Counterparty = strings.TrimSpace(in.Counterparty)
	c.Subject = strings.TrimSpace(in.Subject)
	c.DueDate = in.DueDate
	c.Basis = strings.TrimSpace(in.Basis)
	c.Owner = strings.TrimSpace(in.Owner)
	c.FeatureID = in.FeatureID
	c.ReleaseID = in.ReleaseID
}

// Create создаёт активное обязательство продукта. Право: ActionWriteCommitments по продукту.
func (s *Service) Create(ctx context.Context, sc authz.Scope, productID kernel.ID, in Input) (Commitment, error) {
	if productID == kernel.NilID {
		return Commitment{}, kernel.Invalid("product_id", "обязателен")
	}
	if err := in.validate(); err != nil {
		return Commitment{}, err
	}
	if err := sc.Require(authz.ActionWriteCommitments, productID); err != nil {
		return Commitment{}, err
	}
	now := s.clock.Now()
	c := Commitment{ID: kernel.NewID(), ProductID: productID, Status: StatusActive, CreatedBy: sc.Subject(), CreatedAt: now, UpdatedAt: now}
	in.apply(&c)
	if err := s.store.Save(ctx, c); err != nil {
		return Commitment{}, fmt.Errorf("save commitment: %w", err)
	}
	if err := s.emit(ctx, EventCommitmentCreated, c.ID, c.ProductID, sc.Subject(), c); err != nil {
		return Commitment{}, err
	}
	return c, nil
}

// Update меняет содержание активного обязательства. Продукт и статус не меняются.
func (s *Service) Update(ctx context.Context, sc authz.Scope, id kernel.ID, in Input) (Commitment, error) {
	if err := in.validate(); err != nil {
		return Commitment{}, err
	}
	c, err := s.store.Get(ctx, id)
	if err != nil {
		return Commitment{}, err
	}
	if err := sc.Require(authz.ActionWriteCommitments, c.ProductID); err != nil {
		return Commitment{}, err
	}
	if c.Status != StatusActive {
		return Commitment{}, fmt.Errorf("%w: обязательство в статусе %s не редактируется", kernel.ErrConflict, c.Status)
	}
	in.apply(&c)
	c.UpdatedAt = s.clock.Now()
	if err := s.store.Save(ctx, c); err != nil {
		return Commitment{}, fmt.Errorf("save commitment: %w", err)
	}
	if err := s.emit(ctx, EventCommitmentUpdated, c.ID, c.ProductID, sc.Subject(), c); err != nil {
		return Commitment{}, err
	}
	return c, nil
}

// Get возвращает обязательство. Право: стратегический срез продукта (ТЗ 2.4).
func (s *Service) Get(ctx context.Context, sc authz.Scope, id kernel.ID) (Commitment, error) {
	c, err := s.store.Get(ctx, id)
	if err != nil {
		return Commitment{}, err
	}
	if err := sc.Require(authz.ActionReadStrategic, c.ProductID); err != nil {
		return Commitment{}, err
	}
	return c, nil
}

// List возвращает обязательства по фильтру. С заданным продуктом требует стратегический доступ
// к нему; без продукта — возвращает обязательства всех продуктов, к которым есть такой доступ.
func (s *Service) List(ctx context.Context, sc authz.Scope, f Filter) ([]Commitment, error) {
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	if f.ProductID != kernel.NilID {
		if err := sc.Require(authz.ActionReadStrategic, f.ProductID); err != nil {
			return nil, err
		}
	}
	list, err := s.store.List(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("list commitments: %w", err)
	}
	out := list[:0]
	for _, c := range list {
		if sc.Allows(authz.ActionReadStrategic, c.ProductID) {
			out = append(out, c)
		}
	}
	return out, nil
}

// Fulfil отмечает обязательство исполненным.
func (s *Service) Fulfil(ctx context.Context, sc authz.Scope, id kernel.ID) (Commitment, error) {
	return s.close(ctx, sc, id, StatusFulfilled, EventCommitmentFulfilled)
}

// Cancel отменяет обязательство.
func (s *Service) Cancel(ctx context.Context, sc authz.Scope, id kernel.ID) (Commitment, error) {
	return s.close(ctx, sc, id, StatusCancelled, EventCommitmentCancelled)
}

func (s *Service) close(ctx context.Context, sc authz.Scope, id kernel.ID, st Status, typ string) (Commitment, error) {
	c, err := s.store.Get(ctx, id)
	if err != nil {
		return Commitment{}, err
	}
	if err := sc.Require(authz.ActionWriteCommitments, c.ProductID); err != nil {
		return Commitment{}, err
	}
	if c.Status == StatusFulfilled || c.Status == StatusCancelled {
		return Commitment{}, fmt.Errorf("%w: обязательство уже в статусе %s", kernel.ErrConflict, c.Status)
	}
	c.Status = st
	c.UpdatedAt = s.clock.Now()
	if err := s.store.Save(ctx, c); err != nil {
		return Commitment{}, fmt.Errorf("save commitment: %w", err)
	}
	if err := s.emit(ctx, typ, c.ID, c.ProductID, sc.Subject(), c); err != nil {
		return Commitment{}, err
	}
	return c, nil
}

// ---- CT-03: порт portfoliograph.CommitmentChecker ----

// AffectedCommitments возвращает активные обязательства, привязанные к затронутым фичам,
// срок которых раньше подразумеваемой даты фичи (ImpliedDate). Вызывается portfoliograph
// внутри распространения сдвига (PG-08); авторизация выполнена вызывающей стороной.
func (s *Service) AffectedCommitments(ctx context.Context, affected []portfoliograph.AffectedFeature, _ []kernel.ID) ([]kernel.ID, error) {
	var out []kernel.ID
	for _, a := range affected {
		if a.ImpliedDate.IsZero() {
			continue
		}
		list, err := s.store.List(ctx, Filter{FeatureID: a.FeatureID, Statuses: []Status{StatusActive}})
		if err != nil {
			return nil, fmt.Errorf("list commitments: %w", err)
		}
		for _, c := range list {
			if c.DueDate.Before(a.ImpliedDate) {
				out = append(out, c.ID)
			}
		}
	}
	return out, nil
}

// Alerts возвращает алерты продукта; onlyOpen — только неподтверждённые. Право: стратегический срез.
func (s *Service) Alerts(ctx context.Context, sc authz.Scope, productID kernel.ID, onlyOpen bool) ([]Alert, error) {
	if err := sc.Require(authz.ActionReadStrategic, productID); err != nil {
		return nil, err
	}
	list, err := s.store.Alerts(ctx, productID, onlyOpen)
	if err != nil {
		return nil, fmt.Errorf("list alerts: %w", err)
	}
	return list, nil
}

// AcknowledgeAlert подтверждает алерт. Право: ActionWriteCommitments по продукту.
func (s *Service) AcknowledgeAlert(ctx context.Context, sc authz.Scope, id kernel.ID) (Alert, error) {
	a, err := s.store.Alert(ctx, id)
	if err != nil {
		return Alert{}, err
	}
	if err := sc.Require(authz.ActionWriteCommitments, a.ProductID); err != nil {
		return Alert{}, err
	}
	if a.Acknowledged {
		return a, nil
	}
	a.Acknowledged, a.AcknowledgedBy, a.AcknowledgedAt = true, sc.Subject(), s.clock.Now()
	if err := s.store.Acknowledge(ctx, a); err != nil {
		return Alert{}, fmt.Errorf("acknowledge alert: %w", err)
	}
	if err := s.emit(ctx, EventAlertAcknowledged, a.ID, a.ProductID, sc.Subject(), a); err != nil {
		return Alert{}, err
	}
	return a, nil
}

// raiseAlert создаёт алерт «сдвиг roadmap нарушает обязательство», если newDate позже срока
// активного обязательства. Возвращает true, если алерт поднят.
//
// Дедупликация по паре «обязательство + событие»: одно событие поднимает по обязательству не
// больше одного алерта. Отметка обработанного события ставится только в конце обработчика, поэтому
// сбой в середине приводит к повтору доставки; без этой проверки повтор добавил бы второй алерт в
// append-only список (CT-03). В PostgreSQL то же гарантирует частичный уникальный индекс
// (commitment_id, event_id): гонка двух воркеров даёт kernel.ErrConflict, и событие повторяется.
func (s *Service) raiseAlert(ctx context.Context, sc authz.Scope, c Commitment, newDate kernel.Date, eventID kernel.ID, reason string) (bool, error) {
	if c.Status != StatusActive || newDate.IsZero() || !newDate.After(c.DueDate) {
		return false, nil
	}
	if eventID != kernel.NilID {
		switch _, err := s.store.AlertByEvent(ctx, c.ID, eventID); {
		case err == nil:
			return false, nil
		case kernel.IsNotFound(err):
		default:
			return false, fmt.Errorf("alert by event: %w", err)
		}
	}
	a := Alert{
		ID: kernel.NewID(), CommitmentID: c.ID, ProductID: c.ProductID, Kind: AlertRoadmapShift,
		Message: fmt.Sprintf("сдвиг roadmap на %s нарушает обязательство «%s» перед %s со сроком %s: %s",
			newDate, c.Subject, c.Counterparty, c.DueDate, reason),
		EventID: eventID, NewDate: newDate, DueDate: c.DueDate, RaisedAt: s.clock.Now(),
	}
	if err := s.store.AppendAlert(ctx, a); err != nil {
		return false, fmt.Errorf("append alert: %w", err)
	}
	if err := s.emit(ctx, EventAlertRaised, a.ID, a.ProductID, sc.Subject(), a); err != nil {
		return false, err
	}
	return true, nil
}

// ---- CT-04: продление сертификата ----

// Settings возвращает настройки модуля.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	st, err := s.store.Settings(ctx)
	if err != nil {
		return Settings{}, fmt.Errorf("settings: %w", err)
	}
	if st.LeadMonths <= 0 {
		st.LeadMonths = DefaultLeadMonths
	}
	return st, nil
}

// UpdateSettings меняет настройки (admin).
func (s *Service) UpdateSettings(ctx context.Context, sc authz.Scope, st Settings) error {
	if err := sc.Require(authz.ActionAdminSettings, kernel.NilID); err != nil {
		return err
	}
	if st.LeadMonths <= 0 {
		return kernel.Invalid("lead_months", "срок упреждения должен быть положительным")
	}
	if err := s.store.SaveSettings(ctx, st); err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	return nil
}

// RenewalTitle — название элемента roadmap на продление сертификата.
func RenewalTitle(c Commitment) string {
	return "Продление сертификата: " + c.Subject
}

// EnsureRenewals для каждого активного обязательства certificate_expiry, до срока которого
// осталось не больше Settings.LeadMonths, один раз создаёт элемент roadmap через порт
// RoadmapWriter с датами [now, DueDate−1 день] (CT-04). Обязательства продуктов, на которые
// у Scope нет права записи, пропускаются. Возвращает обязательства, для которых элемент создан
// этим вызовом.
func (s *Service) EnsureRenewals(ctx context.Context, sc authz.Scope, now kernel.Date) ([]Commitment, error) {
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	if now.IsZero() {
		return nil, kernel.Invalid("now", "дата обязательна")
	}
	if s.writer == nil {
		return nil, fmt.Errorf("%w: порт RoadmapWriter не подключён", kernel.ErrUnavailable)
	}
	st, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	horizon := kernel.DateFromTime(now.Time().AddDate(0, st.LeadMonths, 0))
	list, err := s.store.List(ctx, Filter{Kind: KindRegulatory, Subtype: SubtypeCertificateExpiry, Statuses: []Status{StatusActive}, DueBefore: horizon})
	if err != nil {
		return nil, fmt.Errorf("list commitments: %w", err)
	}
	var created []Commitment
	for _, c := range list {
		if c.RenewalItemID != kernel.NilID || !sc.Allows(authz.ActionWriteCommitments, c.ProductID) {
			continue
		}
		end := c.DueDate.AddDays(-1)
		if end.Before(now) {
			// TODO(question-14): сертификат истекает сегодня или уже истёк — элемент однодневный.
			end = now
		}
		itemID, err := s.writer.EnsureRenewalItem(ctx, sc, c.ProductID, c.ID, RenewalTitle(c), now, end)
		if err != nil {
			return created, fmt.Errorf("renewal item for %s: %w", c.ID, err)
		}
		c.RenewalItemID = itemID
		c.UpdatedAt = s.clock.Now()
		if err := s.store.Save(ctx, c); err != nil {
			return created, fmt.Errorf("save commitment: %w", err)
		}
		if err := s.emit(ctx, EventRenewalPlanned, c.ID, c.ProductID, sc.Subject(), c); err != nil {
			return created, err
		}
		created = append(created, c)
	}
	return created, nil
}
