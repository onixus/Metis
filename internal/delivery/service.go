package delivery

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/portfoliograph"
	"github.com/onixus/metis/internal/ports"
)

// Portfolio — публичный интерфейс portfoliograph, нужный модулю; реализуется *portfoliograph.Service.
type Portfolio interface {
	Feature(ctx context.Context, sc authz.Scope, id kernel.ID) (portfoliograph.Feature, error)
	UpdateFeature(ctx context.Context, sc authz.Scope, id kernel.ID, in portfoliograph.FeatureInput) (portfoliograph.Feature, error)
	ShiftFeatureDate(ctx context.Context, sc authz.Scope, id kernel.ID, newDate kernel.Date, reason string) (portfoliograph.ShiftResult, error)
}

// Config — параметры коннектора.
type Config struct {
	// Name — имя коннектора для администратора.
	Name string
	// StaleAfter — порог устаревания проекции (NF-R05). По умолчанию 15 минут.
	StaleAfter time.Duration
	// ServiceScope — область доступа сервисной учётки для сверки и webhook; создаётся identityaccess.
	ServiceScope authz.Scope
}

// Service — публичный интерфейс модуля delivery.
type Service struct {
	store     Store
	tracker   ports.DeliveryTracker
	portfolio Portfolio
	pub       kernel.Publisher
	clock     kernel.Clock
	dlq       DLQReader
	cfg       Config
}

// NewService создаёт сервис. tracker используется только для чтения (Sync); запись — через обработчик outbox.
func NewService(store Store, tracker ports.DeliveryTracker, portfolio Portfolio, pub kernel.Publisher, clock kernel.Clock, cfg Config) *Service {
	if cfg.StaleAfter <= 0 {
		cfg.StaleAfter = 15 * time.Minute
	}
	if cfg.Name == "" {
		cfg.Name = "delivery-tracker"
	}
	return &Service{store: store, tracker: tracker, portfolio: portfolio, pub: pub, clock: clock, cfg: cfg}
}

// WithDLQ подключает счётчик DLQ outbox (AD-05).
func (s *Service) WithDLQ(r DLQReader) *Service {
	s.dlq = r
	return s
}

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

// --- DL-01: маппинг и создание эпика ---

// MapFeature привязывает фичу к эпику: запись в таблицу маппинга и ExternalKey фичи.
func (s *Service) MapFeature(ctx context.Context, sc authz.Scope, featureID kernel.ID, epicKey, project string) (Mapping, error) {
	if epicKey == "" {
		return Mapping{}, kernel.Invalid("epic_key", "обязателен")
	}
	f, err := s.portfolio.Feature(ctx, sc, featureID)
	if err != nil {
		return Mapping{}, err
	}
	if err := sc.Require(authz.ActionWriteGraph, f.ProductID); err != nil {
		return Mapping{}, err
	}
	if existing, err := s.store.MappingByEpic(ctx, epicKey); err == nil && existing.FeatureID != featureID {
		return Mapping{}, fmt.Errorf("%w: эпик %s уже привязан к другой фиче", kernel.ErrConflict, epicKey)
	}
	m := Mapping{FeatureID: featureID, ProductID: f.ProductID, EpicKey: epicKey, Project: project, CreatedAt: s.clock.Now()}
	if err := s.store.SaveMapping(ctx, m); err != nil {
		return Mapping{}, fmt.Errorf("save mapping: %w", err)
	}
	if f.ExternalKey != epicKey {
		if _, err := s.portfolio.UpdateFeature(ctx, sc, featureID, portfoliograph.FeatureInput{ExternalKey: epicKey}); err != nil {
			return Mapping{}, fmt.Errorf("external key: %w", err)
		}
	}
	if err := s.emit(ctx, EventEpicLinked, featureID, f.ProductID, sc.Subject(), m); err != nil {
		return Mapping{}, err
	}
	return m, nil
}

// MapRelease привязывает релиз к версии трекера.
func (s *Service) MapRelease(ctx context.Context, sc authz.Scope, releaseID, productID kernel.ID, project, fixVersion string) (ReleaseMapping, error) {
	if err := sc.Require(authz.ActionWriteRoadmap, productID); err != nil {
		return ReleaseMapping{}, err
	}
	if fixVersion == "" {
		return ReleaseMapping{}, kernel.Invalid("fix_version", "обязательна")
	}
	m := ReleaseMapping{ReleaseID: releaseID, ProductID: productID, Project: project, FixVersion: fixVersion, CreatedAt: s.clock.Now()}
	if err := s.store.SaveReleaseMapping(ctx, m); err != nil {
		return ReleaseMapping{}, fmt.Errorf("save release mapping: %w", err)
	}
	return m, nil
}

// Mappings возвращает привязки фич продукта к эпикам.
func (s *Service) Mappings(ctx context.Context, sc authz.Scope, productID kernel.ID) ([]Mapping, error) {
	if err := sc.Require(authz.ActionReadPrivate, productID); err != nil {
		return nil, err
	}
	all, err := s.store.Mappings(ctx)
	if err != nil {
		return nil, fmt.Errorf("mappings: %w", err)
	}
	out := all[:0:0]
	for _, m := range all {
		if m.ProductID == productID {
			out = append(out, m)
		}
	}
	return out, nil
}

// CreateEpicForFeature запрашивает создание эпика из платформы (DL-01). Во внешнюю систему
// ничего не пишется: публикуется команда EventEpicCreateRequested, которую исполняет
// обработчик outbox в воркере (инвариант 5).
func (s *Service) CreateEpicForFeature(ctx context.Context, sc authz.Scope, featureID kernel.ID, project string) error {
	if project == "" {
		return kernel.Invalid("project", "обязателен")
	}
	f, err := s.portfolio.Feature(ctx, sc, featureID)
	if err != nil {
		return err
	}
	if err := sc.Require(authz.ActionWriteGraph, f.ProductID); err != nil {
		return err
	}
	if f.ExternalKey != "" {
		return fmt.Errorf("%w: фича уже привязана к эпику %s", kernel.ErrConflict, f.ExternalKey)
	}
	if _, err := s.store.MappingByFeature(ctx, featureID); err == nil {
		return fmt.Errorf("%w: фича уже имеет маппинг", kernel.ErrConflict)
	}
	req := EpicCreateRequest{FeatureID: featureID, ProductID: f.ProductID, Project: project, Summary: f.Name}
	return s.emit(ctx, EventEpicCreateRequested, featureID, f.ProductID, sc.Subject(), req)
}

// --- чтение проекции ---

// FeatureProjection возвращает проекцию эпика фичи и состояние синхронизации (признак устаревания, NF-R05).
func (s *Service) FeatureProjection(ctx context.Context, sc authz.Scope, featureID kernel.ID) (EpicProjection, SyncState, error) {
	p, err := s.store.EpicByFeature(ctx, featureID)
	if err != nil {
		return EpicProjection{}, SyncState{}, err
	}
	if err := sc.Require(authz.ActionReadPrivate, p.ProductID); err != nil {
		return EpicProjection{}, SyncState{}, err
	}
	st, err := s.syncState(ctx)
	if err != nil {
		return EpicProjection{}, SyncState{}, err
	}
	return p, st, nil
}

// FeatureMetrics — готовность, scope creep и план/факт фичи (DL-03).
func (s *Service) FeatureMetrics(ctx context.Context, sc authz.Scope, featureID kernel.ID) (FeatureMetrics, error) {
	p, st, err := s.FeatureProjection(ctx, sc, featureID)
	if err != nil {
		return FeatureMetrics{}, err
	}
	f, err := s.portfolio.Feature(ctx, sc, featureID)
	if err != nil {
		return FeatureMetrics{}, err
	}
	return computeMetrics(p, f, st), nil
}

func computeMetrics(p EpicProjection, f portfoliograph.Feature, st SyncState) FeatureMetrics {
	m := FeatureMetrics{FeatureID: p.FeatureID, ProductID: p.ProductID, EpicKey: p.EpicKey, Sync: st}
	for _, is := range p.Issues {
		m.Readiness.Total++
		if is.Done {
			m.Readiness.Done++
		}
	}
	if m.Readiness.Total > 0 {
		m.Readiness.Percent = 100 * float64(m.Readiness.Done) / float64(m.Readiness.Total)
	}
	initial := map[string]bool{}
	for _, k := range p.InitialScope {
		initial[k] = true
	}
	current := map[string]bool{}
	for _, is := range p.Issues {
		current[is.Key] = true
		if !initial[is.Key] {
			m.ScopeCreep.Added = append(m.ScopeCreep.Added, is.Key)
		}
	}
	for _, k := range p.InitialScope {
		if !current[k] {
			m.ScopeCreep.Removed = append(m.ScopeCreep.Removed, k)
		}
	}
	m.ScopeCreep.Initial = len(p.InitialScope)
	m.ScopeCreep.SnapshotAt = p.FirstSeenAt
	if m.ScopeCreep.Initial > 0 {
		m.ScopeCreep.Percent = 100 * float64(len(m.ScopeCreep.Added)) / float64(m.ScopeCreep.Initial)
	}
	m.PlanFact = PlanFact{PlannedDate: f.PlannedDate, DueDate: p.DueDate}
	if !f.PlannedDate.IsZero() && !p.DueDate.IsZero() {
		m.PlanFact.DeltaDays = f.PlannedDate.DaysUntil(p.DueDate)
		m.PlanFact.Late = m.PlanFact.DeltaDays > 0
	}
	return m
}

// SprintStatuses — статусы спринтов доски продукта (DL-02) с признаком устаревания.
func (s *Service) SprintStatuses(ctx context.Context, sc authz.Scope, productID kernel.ID) ([]SprintStatus, SyncState, error) {
	if err := sc.Require(authz.ActionReadPrivate, productID); err != nil {
		return nil, SyncState{}, err
	}
	sp, err := s.store.Sprints(ctx, productID)
	if err != nil {
		return nil, SyncState{}, fmt.Errorf("sprints: %w", err)
	}
	st, err := s.syncState(ctx)
	if err != nil {
		return nil, SyncState{}, err
	}
	return sp, st, nil
}

// SyncState — состояние синхронизации (NF-R05).
func (s *Service) SyncState(ctx context.Context) (SyncState, error) { return s.syncState(ctx) }

func (s *Service) syncState(ctx context.Context) (SyncState, error) {
	st, err := s.store.SyncState(ctx)
	if err != nil {
		return SyncState{}, fmt.Errorf("sync state: %w", err)
	}
	now := s.clock.Now()
	if st.LastSuccessAt.IsZero() {
		st.Stale = true
		return st, nil
	}
	st.Lag = now.Sub(st.LastSuccessAt)
	st.Stale = st.Lag > s.cfg.StaleAfter
	return st, nil
}

// --- синхронизация ---

// Sync — сверка по расписанию через порт (ТЗ 4.2): обновляет проекции эпиков и спринтов.
// При недоступности трекера проекция сохраняется, а состояние получает ошибку (NF-R05).
func (s *Service) Sync(ctx context.Context) error {
	if s.tracker == nil {
		return fmt.Errorf("%w: адаптер delivery выключен", kernel.ErrUnavailable)
	}
	now := s.clock.Now()
	st, err := s.store.SyncState(ctx)
	if err != nil {
		return fmt.Errorf("sync state: %w", err)
	}
	st.LastAttemptAt = now
	if err := s.sync(ctx, now); err != nil {
		st.LastError = err.Error()
		if saveErr := s.store.SaveSyncState(ctx, st); saveErr != nil {
			return fmt.Errorf("save sync state: %w", saveErr)
		}
		return fmt.Errorf("delivery sync: %w", err)
	}
	st.LastSuccessAt, st.LastError = now, ""
	if err := s.store.SaveSyncState(ctx, st); err != nil {
		return fmt.Errorf("save sync state: %w", err)
	}
	return nil
}

// RecordSyncFailure сохраняет состояние неудачной сверки после отката её
// транзакции. Runtime вызывает отдельно: частичная проекция не фиксируется,
// но администратор видит время попытки и ошибку (NF-R05, AD-05).
func (s *Service) RecordSyncFailure(ctx context.Context, attemptedAt time.Time, cause error) error {
	if !s.cfg.ServiceScope.Valid() || !s.cfg.ServiceScope.HasRole(authz.RoleService) {
		return kernel.ErrForbidden
	}
	if cause == nil || attemptedAt.IsZero() {
		return kernel.Invalid("cause", "ошибка и время сверки обязательны")
	}
	st, err := s.store.SyncState(ctx)
	if err != nil {
		return fmt.Errorf("sync state: %w", err)
	}
	if st.LastAttemptAt.After(attemptedAt) {
		return nil // другой воркер уже выполнил более новую сверку
	}
	st.LastAttemptAt, st.LastError = attemptedAt.UTC(), cause.Error()
	if err := s.store.SaveSyncState(ctx, st); err != nil {
		return fmt.Errorf("save failed sync state: %w", err)
	}
	return nil
}

func (s *Service) sync(ctx context.Context, now time.Time) error {
	mappings, err := s.store.Mappings(ctx)
	if err != nil {
		return fmt.Errorf("mappings: %w", err)
	}
	for _, m := range mappings {
		epic, err := s.tracker.Epic(ctx, m.EpicKey)
		if err != nil {
			return fmt.Errorf("epic %s: %w", m.EpicKey, err)
		}
		issues, err := s.tracker.EpicIssues(ctx, m.EpicKey)
		if err != nil {
			return fmt.Errorf("issues %s: %w", m.EpicKey, err)
		}
		if err := s.applyEpic(ctx, m, epic, issues, now, ""); err != nil {
			return err
		}
	}
	fm, err := s.store.FieldMapping(ctx)
	if err != nil {
		return fmt.Errorf("field mapping: %w", err)
	}
	for productID, board := range fm.Boards {
		sprints, err := s.tracker.Sprints(ctx, board)
		if err != nil {
			return fmt.Errorf("sprints %s: %w", board, err)
		}
		if err := s.store.SaveSprints(ctx, productID, sprintStatuses(productID, board, sprints, now)); err != nil {
			return fmt.Errorf("save sprints: %w", err)
		}
	}
	return nil
}

// applyEpic обновляет проекцию эпика; при изменении due date сдвигает плановую дату фичи (7.7 п.5).
func (s *Service) applyEpic(ctx context.Context, m Mapping, epic ports.Epic, issues []ports.Issue, now time.Time, sourceEvent string) error {
	prev, err := s.store.EpicByFeature(ctx, m.FeatureID)
	first := kernel.IsNotFound(err)
	if err != nil && !first {
		return fmt.Errorf("epic projection: %w", err)
	}
	p := EpicProjection{
		FeatureID: m.FeatureID, ProductID: m.ProductID, EpicKey: epic.Key, Summary: epic.Summary, Status: epic.Status,
		DueDate: epic.DueDate, FixVersions: append([]string(nil), epic.FixVersions...), SyncedAt: now, SourceEventID: sourceEvent,
	}
	if issues != nil {
		p.Issues = make([]IssueSnapshot, 0, len(issues))
		for _, is := range issues {
			p.Issues = append(p.Issues, IssueSnapshot{Key: is.Key, Summary: is.Summary, Status: is.Status, Done: is.Done, CreatedAt: is.CreatedAt})
		}
	}
	if first {
		p.FirstSeenAt = now
		for _, is := range p.Issues {
			p.InitialScope = append(p.InitialScope, is.Key)
		}
	} else {
		p.FirstSeenAt, p.InitialScope = prev.FirstSeenAt, prev.InitialScope
		if issues == nil {
			p.Issues = prev.Issues
		}
	}
	if err := s.store.SaveEpic(ctx, p); err != nil {
		return fmt.Errorf("save epic: %w", err)
	}
	if !p.DueDate.IsZero() && (first || prev.DueDate != p.DueDate) {
		if err := s.shiftFeature(ctx, m, prev.DueDate, p.DueDate); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) shiftFeature(ctx context.Context, m Mapping, oldDate, newDate kernel.Date) error {
	f, err := s.portfolio.Feature(ctx, s.cfg.ServiceScope, m.FeatureID)
	if err != nil {
		return fmt.Errorf("feature %s: %w", m.FeatureID, err)
	}
	if f.PlannedDate == newDate {
		return nil
	}
	reason := fmt.Sprintf("эпик %s сдвинут в трекере", m.EpicKey)
	if _, err := s.portfolio.ShiftFeatureDate(ctx, s.cfg.ServiceScope, m.FeatureID, newDate, reason); err != nil {
		return fmt.Errorf("shift feature %s: %w", m.FeatureID, err)
	}
	return s.emit(ctx, EventEpicDueDateChanged, m.FeatureID, m.ProductID, s.cfg.ServiceScope.Subject(),
		map[string]any{"epic_key": m.EpicKey, "old_due_date": oldDate, "new_due_date": newDate})
}

// sprintStatuses строит статусы спринтов (DL-02): прогресс и перенос из предыдущего спринта доски.
func sprintStatuses(productID kernel.ID, board string, sprints []ports.Sprint, now time.Time) []SprintStatus {
	ordered := append([]ports.Sprint(nil), sprints...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].StartDate != ordered[j].StartDate {
			return ordered[i].StartDate.Before(ordered[j].StartDate)
		}
		return ordered[i].ID < ordered[j].ID
	})
	out := make([]SprintStatus, 0, len(ordered))
	var prevKeys map[string]bool
	for _, sp := range ordered {
		st := SprintStatus{
			ProductID: productID, Board: board, SprintID: sp.ID, Name: sp.Name, Goal: sp.Goal, State: sp.State,
			StartDate: sp.StartDate, EndDate: sp.EndDate, SyncedAt: now,
		}
		keys := map[string]bool{}
		for _, is := range sp.Issues {
			st.Issues = append(st.Issues, IssueSnapshot{Key: is.Key, Summary: is.Summary, Status: is.Status, Done: is.Done, CreatedAt: is.CreatedAt})
			st.Total++
			if is.Done {
				st.Done++
			}
			keys[is.Key] = true
			if prevKeys[is.Key] {
				st.CarriedOver = append(st.CarriedOver, is.Key)
			}
		}
		prevKeys = keys
		out = append(out, st)
	}
	return out
}

// HandleWebhook применяет входящее событие трекера. Идемпотентно по ev.ExternalID (NF-R06, ТЗ 4.2):
// повторная доставка ничего не меняет. Сдвиг due date привязанного эпика синхронно сдвигает
// плановую дату фичи через portfoliograph (NF-P03, 7.7 п.5).
func (s *Service) HandleWebhook(ctx context.Context, ev ports.WebhookEvent) error {
	if ev.ExternalID == "" || ev.EpicKey == "" {
		return kernel.Invalid("event", "нужны внешний ключ события и ключ эпика")
	}
	fresh, err := s.store.MarkProcessed(ctx, "webhook:"+ev.ExternalID)
	if err != nil {
		return fmt.Errorf("mark processed: %w", err)
	}
	if !fresh {
		return nil
	}
	m, err := s.store.MappingByEpic(ctx, ev.EpicKey)
	if kernel.IsNotFound(err) {
		return nil // эпик не привязан к фиче — событие не для нас
	}
	if err != nil {
		return fmt.Errorf("mapping: %w", err)
	}
	if ev.Type == ports.WebhookEpicDeleted {
		return nil // удаление в трекере проекцию не стирает: последняя проекция остаётся (NF-R05)
	}
	prev, err := s.store.EpicByFeature(ctx, m.FeatureID)
	if err != nil && !kernel.IsNotFound(err) {
		return fmt.Errorf("epic projection: %w", err)
	}
	epic := ports.Epic{Key: m.EpicKey, Summary: prev.Summary, Status: prev.Status, DueDate: prev.DueDate, FixVersions: prev.FixVersions}
	for _, f := range ev.ChangedFields {
		switch f {
		case "due_date":
			epic.DueDate = ev.NewDueDate
		case "status":
			epic.Status = ev.NewStatus
		}
	}
	if !ev.NewDueDate.IsZero() && epic.DueDate.IsZero() {
		epic.DueDate = ev.NewDueDate
	}
	return s.applyEpic(ctx, m, epic, nil, s.clock.Now(), ev.ExternalID)
}

// --- AD-05: администрирование коннектора ---

// ConnectorStatus — маппинг, состояние синхронизации и DLQ. Только ActionManageConnects.
func (s *Service) ConnectorStatus(ctx context.Context, sc authz.Scope) (ConnectorStatus, error) {
	if err := sc.Require(authz.ActionManageConnects, kernel.NilID); err != nil {
		return ConnectorStatus{}, err
	}
	fm, err := s.store.FieldMapping(ctx)
	if err != nil {
		return ConnectorStatus{}, fmt.Errorf("field mapping: %w", err)
	}
	st, err := s.syncState(ctx)
	if err != nil {
		return ConnectorStatus{}, err
	}
	mappings, err := s.store.Mappings(ctx)
	if err != nil {
		return ConnectorStatus{}, fmt.Errorf("mappings: %w", err)
	}
	cs := ConnectorStatus{Name: s.cfg.Name, Mapping: fm, Sync: st, MappedEpics: len(mappings), StaleAfter: s.cfg.StaleAfter}
	if s.dlq != nil {
		n, err := s.dlq.DLQCount(ctx)
		if err != nil {
			return ConnectorStatus{}, fmt.Errorf("dlq: %w", err)
		}
		cs.DLQCount = n
	}
	return cs, nil
}

// SetFieldMapping задаёт маппинг полей и статусов без изменения кода (ТЗ 4.2). Только ActionManageConnects.
func (s *Service) SetFieldMapping(ctx context.Context, sc authz.Scope, fm FieldMapping) error {
	if err := sc.Require(authz.ActionManageConnects, kernel.NilID); err != nil {
		return err
	}
	if fm.EpicIssueType == "" {
		return kernel.Invalid("epic_issue_type", "обязателен")
	}
	for tracker, status := range fm.StatusMap {
		switch status {
		case portfoliograph.FeatureIdea, portfoliograph.FeatureDiscovery, portfoliograph.FeaturePlanned,
			portfoliograph.FeatureInProgress, portfoliograph.FeatureDone, portfoliograph.FeatureRejected:
		default:
			return kernel.Invalid("status_map", fmt.Sprintf("неизвестный статус фичи %q для %q", status, tracker))
		}
	}
	if fm.Boards == nil {
		fm.Boards = map[kernel.ID]string{}
	}
	if err := s.store.SaveFieldMapping(ctx, fm); err != nil {
		return fmt.Errorf("save field mapping: %w", err)
	}
	return nil
}
