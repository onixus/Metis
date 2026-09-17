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
	store Store
	pub   kernel.Publisher
	clock kernel.Clock
}

// NewService создаёт сервис. Publisher может быть nil (события не публикуются).
func NewService(store Store, pub kernel.Publisher, clock kernel.Clock) *Service {
	if clock == nil {
		clock = kernel.SystemClock{}
	}
	return &Service{store: store, pub: pub, clock: clock}
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
	if err := s.validateItem(ctx, productID, in); err != nil {
		return RoadmapItem{}, err
	}
	now := s.clock.Now()
	it := RoadmapItem{
		ID: kernel.NewID(), ProductID: productID, FeatureID: in.FeatureID, Title: in.Title, Bucket: in.Bucket,
		StartDate: in.StartDate, EndDate: in.EndDate, ReleaseID: in.ReleaseID, Audience: in.Audience, Status: in.Status,
		CreatedAt: now, UpdatedAt: now,
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
	if err := s.validateItem(ctx, it.ProductID, in); err != nil {
		return RoadmapItem{}, err
	}
	it.FeatureID, it.Title, it.Bucket, it.ReleaseID, it.Audience, it.Status =
		in.FeatureID, in.Title, in.Bucket, in.ReleaseID, in.Audience, in.Status
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
	rels, err := s.store.Releases(ctx, productID)
	if err != nil {
		return ByRelease{}, fmt.Errorf("releases: %w", err)
	}
	sortReleases(rels)
	by := map[kernel.ID][]RoadmapItem{}
	for _, it := range items {
		by[it.ReleaseID] = append(by[it.ReleaseID], it)
	}
	v := ByRelease{ProductID: productID, Audience: sc.Audience(), Releases: make([]ReleaseGroup, 0, len(rels))}
	for _, r := range rels {
		g := ReleaseGroup{Release: r}
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

// ---- Релизы ----

// ReleaseInput — данные релиза.
type ReleaseInput struct {
	Name        string
	Version     string
	PlannedDate kernel.Date
	Status      ReleaseStatus
}

// CreateRelease создаёт релиз продукта. Требует ActionWriteRoadmap.
func (s *Service) CreateRelease(ctx context.Context, sc authz.Scope, productID kernel.ID, in ReleaseInput) (Release, error) {
	if err := sc.Require(authz.ActionWriteRoadmap, productID); err != nil {
		return Release{}, err
	}
	if in.Status == "" {
		in.Status = ReleasePlanned
	}
	if strings.TrimSpace(in.Name) == "" {
		return Release{}, kernel.Invalid("name", "название обязательно")
	}
	if strings.TrimSpace(in.Version) == "" {
		return Release{}, kernel.Invalid("version", "версия обязательна")
	}
	if !in.Status.valid() {
		return Release{}, kernel.Invalid("status", "допустимы planned, released, eol")
	}
	existing, err := s.store.Releases(ctx, productID)
	if err != nil {
		return Release{}, fmt.Errorf("releases: %w", err)
	}
	for _, r := range existing {
		if r.Version == in.Version {
			return Release{}, fmt.Errorf("%w: версия %q уже есть у продукта", kernel.ErrConflict, in.Version)
		}
	}
	now := s.clock.Now()
	r := Release{ID: kernel.NewID(), ProductID: productID, Name: in.Name, Version: in.Version,
		PlannedDate: in.PlannedDate, Status: in.Status, CreatedAt: now, UpdatedAt: now}
	if err := s.store.SaveRelease(ctx, r); err != nil {
		return Release{}, fmt.Errorf("save release: %w", err)
	}
	if err := s.emit(ctx, EventReleaseSaved, r.ID, r.ProductID, sc.Subject(), r); err != nil {
		return Release{}, err
	}
	return r, nil
}

// Releases возвращает релизы продукта по плановой дате. Доступны обеим аудиториям (внутренних полей нет).
func (s *Service) Releases(ctx context.Context, sc authz.Scope, productID kernel.ID) ([]Release, error) {
	if err := sc.Require(authz.ActionReadStrategic, productID); err != nil {
		return nil, err
	}
	rels, err := s.store.Releases(ctx, productID)
	if err != nil {
		return nil, fmt.Errorf("releases: %w", err)
	}
	sortReleases(rels)
	return rels, nil
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
