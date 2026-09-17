package roadmap

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// Service — публичный интерфейс модуля roadmap. Каждый метод принимает authz.Scope;
// нулевой Scope запрещает всё. Аудитория среза берётся только из Scope (RM-02).
type Service struct {
	store     Store
	pub       kernel.Publisher
	clock     kernel.Clock
	contracts ContractReader   // может быть nil: матрица совместимости пуста
	readiness ReadinessChecker // может быть nil: MarkReadyForCertification недоступен
}

// NewService создаёт сервис. Publisher может быть nil (события не публикуются).
func NewService(store Store, pub kernel.Publisher, clock kernel.Clock) *Service {
	if clock == nil {
		clock = kernel.SystemClock{}
	}
	return &Service{store: store, pub: pub, clock: clock}
}

// WithContracts подключает порт контрактов для матрицы совместимости (RM-05).
func (s *Service) WithContracts(c ContractReader) *Service {
	s.contracts = c
	return s
}

// WithReadiness подключает порт готовности к сертификации (RM-05, CM-05).
func (s *Service) WithReadiness(r ReadinessChecker) *Service {
	s.readiness = r
	return s
}

func (s *Service) emit(ctx context.Context, typ string, aggregate, product kernel.ID, actor string, payload any) error {
	if s.pub == nil {
		return nil
	}
	ev, err := kernel.NewEvent(s.clock, typ, aggregate, product, actor, payload)
	if err != nil {
		return err
	}
	if err := s.pub.Publish(ctx, ev); err != nil {
		return fmt.Errorf("publish %s: %w", typ, err)
	}
	return nil
}

// ---- Элементы ----

// ItemInput — данные для создания или изменения элемента roadmap.
type ItemInput struct {
	FeatureID kernel.ID
	Title     string
	Bucket    Bucket
	StartDate kernel.Date
	EndDate   kernel.Date
	ReleaseID kernel.ID
	Audience  authz.Audience
	Status    ItemStatus
	Kind      ItemKind // по умолчанию feature (RM-04)
}

func validateDates(start, end kernel.Date) error {
	if !start.IsZero() && !end.IsZero() && end.Before(start) {
		return kernel.Invalid("end_date", "дата окончания раньше даты начала")
	}
	return nil
}

func (s *Service) validateItem(ctx context.Context, productID kernel.ID, in ItemInput) error {
	if strings.TrimSpace(in.Title) == "" {
		return kernel.Invalid("title", "название обязательно")
	}
	if !in.Bucket.valid() {
		return kernel.Invalid("bucket", "допустимы now, next, later")
	}
	if in.Audience != authz.AudienceInternal && in.Audience != authz.AudienceSalesSafe {
		return kernel.Invalid("audience", "допустимы internal, sales_safe")
	}
	if !in.Status.valid() {
		return kernel.Invalid("status", "недопустимый статус")
	}
	if !in.Kind.valid() {
		return kernel.Invalid("kind", "допустимы feature, fix")
	}
	if err := validateDates(in.StartDate, in.EndDate); err != nil {
		return err
	}
	if in.ReleaseID != kernel.NilID {
		r, err := s.store.Release(ctx, in.ReleaseID)
		if err != nil {
			return fmt.Errorf("release: %w", err)
		}
		if r.ProductID != productID {
			return kernel.Invalid("release_id", "релиз принадлежит другому продукту")
		}
		if r.Branch == BranchCertified && in.Kind != KindFix {
			return fmt.Errorf("%w: релиз %s в сертифицированной ветке принимает только исправления (kind=fix), элемент вида %q привязать нельзя",
				kernel.ErrConflict, r.Version, in.Kind)
		}
	}
	return nil
}

// CreateItem создаёт элемент roadmap продукта. Требует ActionWriteRoadmap.
func (s *Service) CreateItem(ctx context.Context, sc authz.Scope, productID kernel.ID, in ItemInput) (RoadmapItem, error) {
	if err := sc.Require(authz.ActionWriteRoadmap, productID); err != nil {
		return RoadmapItem{}, err
	}
	if in.Status == "" {
		in.Status = ItemPlanned
	}
	if in.Audience == "" {
		in.Audience = authz.AudienceInternal
	}
	if in.Kind == "" {
		in.Kind = KindFeature
	}
	if err := s.validateItem(ctx, productID, in); err != nil {
		return RoadmapItem{}, err
	}
	now := s.clock.Now()
	it := RoadmapItem{
		ID: kernel.NewID(), ProductID: productID, FeatureID: in.FeatureID, Title: in.Title, Bucket: in.Bucket,
		StartDate: in.StartDate, EndDate: in.EndDate, ReleaseID: in.ReleaseID, Audience: in.Audience, Status: in.Status,
		Kind: in.Kind, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.SaveItem(ctx, it); err != nil {
		return RoadmapItem{}, fmt.Errorf("save item: %w", err)
	}
	if err := s.emit(ctx, EventItemSaved, it.ID, it.ProductID, sc.Subject(), it); err != nil {
		return RoadmapItem{}, err
	}
	return it, nil
}

// UpdateItem изменяет атрибуты элемента. Даты через UpdateItem менять нельзя — только ChangeDates с причиной (RM-03).
func (s *Service) UpdateItem(ctx context.Context, sc authz.Scope, id kernel.ID, in ItemInput) (RoadmapItem, error) {
	if !sc.Valid() {
		return RoadmapItem{}, kernel.ErrForbidden
	}
	it, err := s.store.Item(ctx, id)
	if err != nil {
		return RoadmapItem{}, err
	}
	if err := sc.Require(authz.ActionWriteRoadmap, it.ProductID); err != nil {
		return RoadmapItem{}, err
	}
	if in.StartDate != it.StartDate || in.EndDate != it.EndDate {
		return RoadmapItem{}, kernel.Invalid("dates", "изменение дат выполняется через ChangeDates с указанием причины")
	}
	if in.Kind == "" {
		in.Kind = it.Kind
	}
	if err := s.validateItem(ctx, it.ProductID, in); err != nil {
		return RoadmapItem{}, err
	}
	it.FeatureID, it.Title, it.Bucket, it.ReleaseID, it.Audience, it.Status, it.Kind =
		in.FeatureID, in.Title, in.Bucket, in.ReleaseID, in.Audience, in.Status, in.Kind
	it.UpdatedAt = s.clock.Now()
	if err := s.store.SaveItem(ctx, it); err != nil {
		return RoadmapItem{}, fmt.Errorf("save item: %w", err)
	}
	if err := s.emit(ctx, EventItemSaved, it.ID, it.ProductID, sc.Subject(), it); err != nil {
		return RoadmapItem{}, err
	}
	return it, nil
}

// visibleItems возвращает элементы продукта, отфильтрованные по аудитории Scope и отсортированные.
func (s *Service) visibleItems(ctx context.Context, sc authz.Scope, productID kernel.ID) ([]RoadmapItem, error) {
	if err := sc.Require(authz.ActionReadStrategic, productID); err != nil {
		return nil, err
	}
	all, err := s.store.Items(ctx, productID)
	if err != nil {
		return nil, fmt.Errorf("items: %w", err)
	}
	out := make([]RoadmapItem, 0, len(all))
	for _, it := range all {
		if sc.Audience() != authz.AudienceInternal && it.Audience != authz.AudienceSalesSafe {
			continue
		}
		out = append(out, it)
	}
	sortItems(out)
	return out, nil
}

func sortKey(it RoadmapItem) kernel.Date {
	if it.StartDate.IsZero() {
		return it.EndDate
	}
	return it.StartDate
}

func sortItems(items []RoadmapItem) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		ka, kb := sortKey(a), sortKey(b)
		switch {
		case ka.IsZero() != kb.IsZero():
			return !ka.IsZero() // элементы без дат — в конце
		case ka != kb:
			return ka.Before(kb)
		case a.EndDate != b.EndDate:
			if a.EndDate.IsZero() != b.EndDate.IsZero() {
				return !a.EndDate.IsZero()
			}
			return a.EndDate.Before(b.EndDate)
		case a.Title != b.Title:
			return a.Title < b.Title
		}
		return a.ID.String() < b.ID.String()
	})
}

func split(sc authz.Scope, items []RoadmapItem) ([]RoadmapItem, []SalesSafeItem) {
	if sc.Audience() == authz.AudienceInternal {
		return items, nil
	}
	out := make([]SalesSafeItem, 0, len(items))
	for _, it := range items {
		out = append(out, toSalesSafe(it))
	}
	return nil, out
}

// Items возвращает полные элементы продукта. Доступно только внутренней аудитории;
// sales-safe аудитория пользуется представлениями (Timeline, NowNextLater, ByRelease).
func (s *Service) Items(ctx context.Context, sc authz.Scope, productID kernel.ID) ([]RoadmapItem, error) {
	if sc.Audience() != authz.AudienceInternal {
		return nil, fmt.Errorf("%w: полные элементы roadmap доступны только внутренней аудитории", kernel.ErrForbidden)
	}
	return s.visibleItems(ctx, sc, productID)
}

// ---- Представления (RM-01) ----

// Timeline — элементы с датами, отсортированные по дате начала (или окончания, если начало не задано).
func (s *Service) Timeline(ctx context.Context, sc authz.Scope, productID kernel.ID) (Timeline, error) {
	items, err := s.visibleItems(ctx, sc, productID)
	if err != nil {
		return Timeline{}, err
	}
	dated := make([]RoadmapItem, 0, len(items))
	for _, it := range items {
		if !sortKey(it).IsZero() {
			dated = append(dated, it)
		}
	}
	tl := Timeline{ProductID: productID, Audience: sc.Audience()}
	tl.Items, tl.SalesSafe = split(sc, dated)
	return tl, nil
}

// NowNextLater — группировка элементов по корзинам.
func (s *Service) NowNextLater(ctx context.Context, sc authz.Scope, productID kernel.ID) (NowNextLater, error) {
	items, err := s.visibleItems(ctx, sc, productID)
	if err != nil {
		return NowNextLater{}, err
	}
	by := map[Bucket][]RoadmapItem{}
	for _, it := range items {
		by[it.Bucket] = append(by[it.Bucket], it)
	}
	v := NowNextLater{ProductID: productID, Audience: sc.Audience()}
	v.Now.Items, v.Now.SalesSafe = split(sc, by[BucketNow])
	v.Next.Items, v.Next.SalesSafe = split(sc, by[BucketNext])
	v.Later.Items, v.Later.SalesSafe = split(sc, by[BucketLater])
	return v, nil
}

// ByRelease — группировка элементов по релизам продукта; релизы отсортированы по плановой дате.
func (s *Service) ByRelease(ctx context.Context, sc authz.Scope, productID kernel.ID) (ByRelease, error) {
	items, err := s.visibleItems(ctx, sc, productID)
	if err != nil {
		return ByRelease{}, err
	}
	rels, err := s.releasesFor(ctx, sc, productID)
	if err != nil {
		return ByRelease{}, err
	}
	by := map[kernel.ID][]RoadmapItem{}
	for _, it := range items {
		by[it.ReleaseID] = append(by[it.ReleaseID], it)
	}
	v := ByRelease{ProductID: productID, Audience: sc.Audience(), Releases: make([]ReleaseGroup, 0, len(rels))}
	for _, r := range rels {
		g := ReleaseGroup{Release: r}
		if sc.Audience() != authz.AudienceInternal {
			ss := toSalesSafeRelease(r)
			g.SalesSafeRelease = &ss
		}
		g.Items, g.SalesSafe = split(sc, by[r.ID])
		v.Releases = append(v.Releases, g)
	}
	v.Unassigned.Items, v.Unassigned.SalesSafe = split(sc, by[kernel.NilID])
	return v, nil
}

// ---- История дат (RM-03) ----

// ChangeDates меняет даты элемента с обязательной причиной и записывает историю.
func (s *Service) ChangeDates(ctx context.Context, sc authz.Scope, itemID kernel.ID, newStart, newEnd kernel.Date, reason string) (RoadmapItem, error) {
	return s.changeDates(ctx, sc, itemID, newStart, newEnd, reason, kernel.NilID)
}

func (s *Service) changeDates(ctx context.Context, sc authz.Scope, itemID kernel.ID, newStart, newEnd kernel.Date, reason string, eventID kernel.ID) (RoadmapItem, error) {
	if !sc.Valid() {
		return RoadmapItem{}, kernel.ErrForbidden
	}
	if strings.TrimSpace(reason) == "" {
		return RoadmapItem{}, kernel.Invalid("reason", "причина изменения даты обязательна")
	}
	if err := validateDates(newStart, newEnd); err != nil {
		return RoadmapItem{}, err
	}
	it, err := s.store.Item(ctx, itemID)
	if err != nil {
		return RoadmapItem{}, err
	}
	if err := sc.Require(authz.ActionWriteRoadmap, it.ProductID); err != nil {
		return RoadmapItem{}, err
	}
	ch := DateChange{
		ID: kernel.NewID(), ItemID: it.ID, ProductID: it.ProductID,
		OldStart: it.StartDate, OldEnd: it.EndDate, NewStart: newStart, NewEnd: newEnd,
		Reason: reason, Actor: sc.Subject(), At: s.clock.Now(), EventID: eventID,
	}
	it.StartDate, it.EndDate, it.UpdatedAt = newStart, newEnd, ch.At
	if err := s.store.AppendDateChange(ctx, ch); err != nil {
		return RoadmapItem{}, fmt.Errorf("append date change: %w", err)
	}
	if err := s.store.SaveItem(ctx, it); err != nil {
		return RoadmapItem{}, fmt.Errorf("save item: %w", err)
	}
	if err := s.emit(ctx, EventDatesChanged, it.ID, it.ProductID, sc.Subject(), ch); err != nil {
		return RoadmapItem{}, err
	}
	return it, nil
}

// DateHistory возвращает историю изменений дат элемента. История с причинами — внутренняя информация:
// sales-safe аудитории недоступна.
func (s *Service) DateHistory(ctx context.Context, sc authz.Scope, itemID kernel.ID) ([]DateChange, error) {
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	it, err := s.store.Item(ctx, itemID)
	if err != nil {
		return nil, err
	}
	if err := sc.Require(authz.ActionReadStrategic, it.ProductID); err != nil {
		return nil, err
	}
	if sc.Audience() != authz.AudienceInternal {
		return nil, fmt.Errorf("%w: история дат доступна только внутренней аудитории", kernel.ErrForbidden)
	}
	hist, err := s.store.DateHistory(ctx, itemID)
	if err != nil {
		return nil, fmt.Errorf("date history: %w", err)
	}
	return hist, nil
}

// ---- Релизы (RM-04, RM-05) ----

// ReleaseInput — данные релиза.
type ReleaseInput struct {
	Name        string
	Version     string
	PlannedDate kernel.Date
	Status      ReleaseStatus
	// Branch — ветка версии (RM-04); пустое значение — evolving при создании, без изменения при обновлении.
	Branch Branch
	// BaseReleaseID — релиз, от которого ответвлена сертифицированная ветка (опционально).
	BaseReleaseID kernel.ID
	// EOL — дата окончания поддержки (RM-05), опционально.
	EOL kernel.Date
}

func validateReleaseInput(in ReleaseInput) error {
	if strings.TrimSpace(in.Name) == "" {
		return kernel.Invalid("name", "название обязательно")
	}
	if strings.TrimSpace(in.Version) == "" {
		return kernel.Invalid("version", "версия обязательна")
	}
	if !in.Status.valid() {
		return kernel.Invalid("status", "допустимы planned, ready_for_certification, released, eol")
	}
	if !in.Branch.valid() {
		return kernel.Invalid("branch", "допустимы certified, evolving")
	}
	if !in.EOL.IsZero() && !in.PlannedDate.IsZero() && in.EOL.Before(in.PlannedDate) {
		return kernel.Invalid("eol", "дата EOL раньше плановой даты релиза")
	}
	return nil
}

// validateBase проверяет базовый релиз ветки: существует, того же продукта, не сам релиз.
func (s *Service) validateBase(ctx context.Context, productID, selfID, baseID kernel.ID) error {
	if baseID == kernel.NilID {
		return nil
	}
	if baseID == selfID {
		return kernel.Invalid("base_release_id", "релиз не может быть базой самого себя")
	}
	base, err := s.store.Release(ctx, baseID)
	if err != nil {
		return fmt.Errorf("base release: %w", err)
	}
	if base.ProductID != productID {
		return kernel.Invalid("base_release_id", "базовый релиз принадлежит другому продукту")
	}
	return nil
}

// versionTaken проверяет уникальность версии среди релизов продукта (кроме exclude).
func (s *Service) versionTaken(ctx context.Context, productID kernel.ID, version string, exclude kernel.ID) error {
	existing, err := s.store.Releases(ctx, productID)
	if err != nil {
		return fmt.Errorf("releases: %w", err)
	}
	for _, r := range existing {
		if r.ID != exclude && r.Version == version {
			return fmt.Errorf("%w: версия %q уже есть у продукта", kernel.ErrConflict, version)
		}
	}
	return nil
}

// CreateRelease создаёт релиз продукта. Требует ActionWriteRoadmap. Ветка по умолчанию — evolving.
func (s *Service) CreateRelease(ctx context.Context, sc authz.Scope, productID kernel.ID, in ReleaseInput) (Release, error) {
	if err := sc.Require(authz.ActionWriteRoadmap, productID); err != nil {
		return Release{}, err
	}
	if in.Status == "" {
		in.Status = ReleasePlanned
	}
	if in.Branch == "" {
		in.Branch = BranchEvolving
	}
	if err := validateReleaseInput(in); err != nil {
		return Release{}, err
	}
	if err := s.validateBase(ctx, productID, kernel.NilID, in.BaseReleaseID); err != nil {
		return Release{}, err
	}
	if err := s.versionTaken(ctx, productID, in.Version, kernel.NilID); err != nil {
		return Release{}, err
	}
	now := s.clock.Now()
	r := Release{ID: kernel.NewID(), ProductID: productID, Name: in.Name, Version: in.Version,
		PlannedDate: in.PlannedDate, Status: in.Status, Branch: in.Branch, BaseReleaseID: in.BaseReleaseID, EOL: in.EOL,
		CreatedAt: now, UpdatedAt: now}
	if err := s.saveRelease(ctx, sc, r); err != nil {
		return Release{}, err
	}
	return r, nil
}

// released сообщает, выпущен ли релиз (released или eol): после выпуска ветка не меняется.
func released(st ReleaseStatus) bool { return st == ReleaseReleased || st == ReleaseEOL }

// UpdateRelease изменяет атрибуты релиза. Смена ветки после выпуска запрещена (RM-04).
// Статус ready_for_certification выставляется только через MarkReadyForCertification.
func (s *Service) UpdateRelease(ctx context.Context, sc authz.Scope, id kernel.ID, in ReleaseInput) (Release, error) {
	r, err := s.writableRelease(ctx, sc, id)
	if err != nil {
		return Release{}, err
	}
	if in.Status == "" {
		in.Status = r.Status
	}
	if in.Branch == "" {
		in.Branch = r.Branch
	}
	if err := validateReleaseInput(in); err != nil {
		return Release{}, err
	}
	if in.Status == ReleaseReadyForCertification && r.Status != ReleaseReadyForCertification {
		return Release{}, fmt.Errorf("%w: статус ready_for_certification выставляется через MarkReadyForCertification", kernel.ErrConflict)
	}
	if in.Branch != r.Branch && released(r.Status) {
		return Release{}, fmt.Errorf("%w: ветка релиза %s не меняется после выпуска (статус %s)", kernel.ErrConflict, r.Version, r.Status)
	}
	if in.Branch == BranchCertified && r.Branch != BranchCertified {
		if err := s.certifiedAllowed(ctx, r); err != nil {
			return Release{}, err
		}
	}
	if err := s.validateBase(ctx, r.ProductID, r.ID, in.BaseReleaseID); err != nil {
		return Release{}, err
	}
	if in.Version != r.Version {
		if err := s.versionTaken(ctx, r.ProductID, in.Version, r.ID); err != nil {
			return Release{}, err
		}
	}
	r.Name, r.Version, r.PlannedDate, r.Status, r.Branch, r.BaseReleaseID, r.EOL =
		in.Name, in.Version, in.PlannedDate, in.Status, in.Branch, in.BaseReleaseID, in.EOL
	r.UpdatedAt = s.clock.Now()
	if err := s.saveRelease(ctx, sc, r); err != nil {
		return Release{}, err
	}
	return r, nil
}

// certifiedAllowed проверяет, что в релизе нет элементов вида feature — иначе перевод в сертифицированную ветку невозможен.
func (s *Service) certifiedAllowed(ctx context.Context, r Release) error {
	items, err := s.store.Items(ctx, r.ProductID)
	if err != nil {
		return fmt.Errorf("items: %w", err)
	}
	for _, it := range items {
		if it.ReleaseID == r.ID && it.Kind != KindFix {
			return fmt.Errorf("%w: релиз %s содержит элемент %q вида %s; в сертифицированной ветке допустимы только исправления",
				kernel.ErrConflict, r.Version, it.Title, it.Kind)
		}
	}
	return nil
}

// writableRelease загружает релиз и проверяет право записи.
func (s *Service) writableRelease(ctx context.Context, sc authz.Scope, id kernel.ID) (Release, error) {
	if !sc.Valid() {
		return Release{}, kernel.ErrForbidden
	}
	r, err := s.store.Release(ctx, id)
	if err != nil {
		return Release{}, err
	}
	if err := sc.Require(authz.ActionWriteRoadmap, r.ProductID); err != nil {
		return Release{}, err
	}
	return r, nil
}

func (s *Service) saveRelease(ctx context.Context, sc authz.Scope, r Release) error {
	if err := s.store.SaveRelease(ctx, r); err != nil {
		return fmt.Errorf("save release: %w", err)
	}
	return s.emit(ctx, EventReleaseSaved, r.ID, r.ProductID, sc.Subject(), r)
}

// SetReleaseFeatures задаёт состав релиза (RM-05). Дубликаты идентификаторов убираются.
func (s *Service) SetReleaseFeatures(ctx context.Context, sc authz.Scope, releaseID kernel.ID, featureIDs []kernel.ID) (Release, error) {
	r, err := s.writableRelease(ctx, sc, releaseID)
	if err != nil {
		return Release{}, err
	}
	seen := make(map[kernel.ID]struct{}, len(featureIDs))
	ids := make([]kernel.ID, 0, len(featureIDs))
	for _, id := range featureIDs {
		if id == kernel.NilID {
			return Release{}, kernel.Invalid("feature_ids", "пустой идентификатор фичи")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	r.FeatureIDs, r.UpdatedAt = ids, s.clock.Now()
	if err := s.saveRelease(ctx, sc, r); err != nil {
		return Release{}, err
	}
	return r, nil
}

// SetReleaseNotes задаёт release notes (RM-05).
func (s *Service) SetReleaseNotes(ctx context.Context, sc authz.Scope, releaseID kernel.ID, notes string) (Release, error) {
	r, err := s.writableRelease(ctx, sc, releaseID)
	if err != nil {
		return Release{}, err
	}
	r.ReleaseNotes, r.UpdatedAt = notes, s.clock.Now()
	if err := s.saveRelease(ctx, sc, r); err != nil {
		return Release{}, err
	}
	return r, nil
}

// SetReleaseEOL задаёт дату окончания поддержки (RM-05). Нулевая дата снимает EOL.
func (s *Service) SetReleaseEOL(ctx context.Context, sc authz.Scope, releaseID kernel.ID, eol kernel.Date) (Release, error) {
	r, err := s.writableRelease(ctx, sc, releaseID)
	if err != nil {
		return Release{}, err
	}
	if !eol.IsZero() && !r.PlannedDate.IsZero() && eol.Before(r.PlannedDate) {
		return Release{}, kernel.Invalid("eol", "дата EOL раньше плановой даты релиза")
	}
	r.EOL, r.UpdatedAt = eol, s.clock.Now()
	if err := s.saveRelease(ctx, sc, r); err != nil {
		return Release{}, err
	}
	return r, nil
}

// MarkReadyForCertification переводит релиз в статус ready_for_certification, если порт готовности
// (CM-05) подтверждает закрытие чек-листов гейтов SSDLC. Без порта — ErrUnavailable.
// Незакрытые пункты возвращаются в ошибке ErrConflict.
func (s *Service) MarkReadyForCertification(ctx context.Context, sc authz.Scope, releaseID kernel.ID) (Release, error) {
	r, err := s.writableRelease(ctx, sc, releaseID)
	if err != nil {
		return Release{}, err
	}
	if s.readiness == nil {
		return Release{}, fmt.Errorf("%w: проверка готовности к сертификации не подключена", kernel.ErrUnavailable)
	}
	if released(r.Status) {
		return Release{}, fmt.Errorf("%w: релиз %s уже выпущен (статус %s)", kernel.ErrConflict, r.Version, r.Status)
	}
	rd, err := s.readiness.ReleaseReadiness(ctx, sc, releaseID)
	if err != nil {
		return Release{}, fmt.Errorf("release readiness: %w", err)
	}
	if !rd.Ready {
		return Release{}, fmt.Errorf("%w: релиз %s не готов к сертификации, открыто: %s",
			kernel.ErrConflict, r.Version, strings.Join(rd.OpenItems, "; "))
	}
	if r.Status == ReleaseReadyForCertification {
		return r, nil // идемпотентно
	}
	r.Status, r.UpdatedAt = ReleaseReadyForCertification, s.clock.Now()
	if err := s.saveRelease(ctx, sc, r); err != nil {
		return Release{}, err
	}
	if err := s.emit(ctx, EventReleaseReadyForCertification, r.ID, r.ProductID, sc.Subject(), r); err != nil {
		return Release{}, err
	}
	return r, nil
}

// Release возвращает релиз с матрицей совместимости. Sales-safe аудитория получает релиз без release notes и состава.
func (s *Service) Release(ctx context.Context, sc authz.Scope, releaseID kernel.ID) (Release, error) {
	if !sc.Valid() {
		return Release{}, kernel.ErrForbidden
	}
	r, err := s.store.Release(ctx, releaseID)
	if err != nil {
		return Release{}, err
	}
	if err := sc.Require(authz.ActionReadStrategic, r.ProductID); err != nil {
		return Release{}, err
	}
	if r.CompatibilityMatrix, err = s.compatibility(ctx, sc, r); err != nil {
		return Release{}, err
	}
	if sc.Audience() != authz.AudienceInternal {
		r = stripInternal(r)
	}
	return r, nil
}

// CompatibilityMatrix возвращает матрицу совместимости релиза (RM-05). Доступна обеим аудиториям.
func (s *Service) CompatibilityMatrix(ctx context.Context, sc authz.Scope, releaseID kernel.ID) ([]CompatRow, error) {
	r, err := s.Release(ctx, sc, releaseID)
	if err != nil {
		return nil, err
	}
	return r.CompatibilityMatrix, nil
}

// compatibility строит матрицу совместимости из контрактов, где продукт релиза — поставщик или потребитель,
// оставляя пары версий, в которых версия стороны продукта совпадает с версией релиза.
func (s *Service) compatibility(ctx context.Context, sc authz.Scope, r Release) ([]CompatRow, error) {
	if s.contracts == nil {
		return nil, nil
	}
	contracts, err := s.contracts.Contracts(ctx, sc)
	if err != nil {
		return nil, fmt.Errorf("contracts: %w", err)
	}
	var rows []CompatRow
	for _, c := range contracts {
		provider, consumer := c.ProviderProductID == r.ProductID, c.ConsumerProductID == r.ProductID
		if !provider && !consumer {
			continue
		}
		for _, p := range c.Compatibility {
			if (provider && p.ProviderVersion == r.Version) || (consumer && p.ConsumerVersion == r.Version) {
				rows = append(rows, CompatRow{ContractID: c.ID, ContractName: c.Name,
					ProviderProductID: c.ProviderProductID, ConsumerProductID: c.ConsumerProductID,
					ProviderVersion: p.ProviderVersion, ConsumerVersion: p.ConsumerVersion, Compatible: p.Compatible})
			}
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.ContractName != b.ContractName {
			return a.ContractName < b.ContractName
		}
		if a.ContractID != b.ContractID {
			return a.ContractID.String() < b.ContractID.String()
		}
		if a.ProviderVersion != b.ProviderVersion {
			return a.ProviderVersion < b.ProviderVersion
		}
		return a.ConsumerVersion < b.ConsumerVersion
	})
	return rows, nil
}

// releasesFor возвращает релизы продукта с матрицами, отсортированные по дате, в проекции аудитории Scope.
func (s *Service) releasesFor(ctx context.Context, sc authz.Scope, productID kernel.ID) ([]Release, error) {
	rels, err := s.store.Releases(ctx, productID)
	if err != nil {
		return nil, fmt.Errorf("releases: %w", err)
	}
	sortReleases(rels)
	for i := range rels {
		if rels[i].CompatibilityMatrix, err = s.compatibility(ctx, sc, rels[i]); err != nil {
			return nil, err
		}
		if sc.Audience() != authz.AudienceInternal {
			rels[i] = stripInternal(rels[i])
		}
	}
	return rels, nil
}

// Releases возвращает релизы продукта по плановой дате. Sales-safe аудитория получает релизы без
// release notes и состава; матрица совместимости и EOL доступны обеим аудиториям.
func (s *Service) Releases(ctx context.Context, sc authz.Scope, productID kernel.ID) ([]Release, error) {
	if err := sc.Require(authz.ActionReadStrategic, productID); err != nil {
		return nil, err
	}
	return s.releasesFor(ctx, sc, productID)
}

// ---- Порт для commitments (CT-04) ----

// Сроки корзин для элементов, создаваемых по обязательствам: до 90 дней — now, до года — next, иначе later.
const (
	renewalNowDays  = 90
	renewalNextDays = 365
)

func bucketFor(now, end kernel.Date) Bucket {
	if end.IsZero() {
		return BucketLater
	}
	switch days := now.DaysUntil(end); {
	case days <= renewalNowDays:
		return BucketNow
	case days <= renewalNextDays:
		return BucketNext
	default:
		return BucketLater
	}
}

// EnsureRenewalItem создаёт элемент roadmap по обязательству (CT-04: продление сертификата) один раз на
// commitmentID: повторный вызов возвращает существующий элемент. Аудитория internal, вид feature.
// Требует ActionWriteRoadmap.
func (s *Service) EnsureRenewalItem(ctx context.Context, sc authz.Scope, productID, commitmentID kernel.ID, title string, start, end kernel.Date) (kernel.ID, error) {
	if err := sc.Require(authz.ActionWriteRoadmap, productID); err != nil {
		return kernel.NilID, err
	}
	if commitmentID == kernel.NilID {
		return kernel.NilID, kernel.Invalid("commitment_id", "идентификатор обязательства обязателен")
	}
	existing, err := s.store.ItemByCommitment(ctx, commitmentID)
	switch {
	case err == nil:
		if existing.ProductID != productID {
			return kernel.NilID, fmt.Errorf("%w: элемент по обязательству %s принадлежит другому продукту", kernel.ErrConflict, commitmentID)
		}
		return existing.ID, nil
	case !kernel.IsNotFound(err):
		return kernel.NilID, fmt.Errorf("item by commitment: %w", err)
	}
	in := ItemInput{Title: title, Bucket: bucketFor(kernel.DateFromTime(s.clock.Now()), end), StartDate: start, EndDate: end,
		Audience: authz.AudienceInternal, Status: ItemPlanned, Kind: KindFeature}
	if err := s.validateItem(ctx, productID, in); err != nil {
		return kernel.NilID, err
	}
	now := s.clock.Now()
	it := RoadmapItem{ID: kernel.NewID(), ProductID: productID, Title: in.Title, Bucket: in.Bucket, StartDate: start, EndDate: end,
		Audience: in.Audience, Status: in.Status, Kind: in.Kind, CommitmentID: commitmentID, CreatedAt: now, UpdatedAt: now}
	if err := s.store.SaveItem(ctx, it); err != nil {
		return kernel.NilID, fmt.Errorf("save item: %w", err)
	}
	if err := s.emit(ctx, EventItemSaved, it.ID, it.ProductID, sc.Subject(), it); err != nil {
		return kernel.NilID, err
	}
	return it.ID, nil
}

// ItemLinks возвращает привязки элемента roadmap — фичу и релиз (NilID — нет привязки).
// Порт commitments.RoadmapReader (CT-03): обработчику roadmap.EventDatesChanged нужны привязки
// сдвинутого элемента. Право: стратегический срез продукта элемента.
func (s *Service) ItemLinks(ctx context.Context, sc authz.Scope, itemID kernel.ID) (featureID, releaseID kernel.ID, err error) {
	if !sc.Valid() {
		return kernel.NilID, kernel.NilID, kernel.ErrForbidden
	}
	it, err := s.store.Item(ctx, itemID)
	if err != nil {
		return kernel.NilID, kernel.NilID, err
	}
	if err := sc.Require(authz.ActionReadStrategic, it.ProductID); err != nil {
		return kernel.NilID, kernel.NilID, err
	}
	return it.FeatureID, it.ReleaseID, nil
}

func sortReleases(rels []Release) {
	sort.SliceStable(rels, func(i, j int) bool {
		a, b := rels[i], rels[j]
		if a.PlannedDate.IsZero() != b.PlannedDate.IsZero() {
			return !a.PlannedDate.IsZero()
		}
		if a.PlannedDate != b.PlannedDate {
			return a.PlannedDate.Before(b.PlannedDate)
		}
		if a.Version != b.Version {
			return a.Version < b.Version
		}
		return a.ID.String() < b.ID.String()
	})
}
