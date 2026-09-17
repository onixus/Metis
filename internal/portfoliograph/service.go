package portfoliograph

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// CommitmentChecker — порт модуля обязательств (этап 2): какие обязательства нарушает сдвиг.
type CommitmentChecker interface {
	AffectedCommitments(ctx context.Context, affected []AffectedFeature, contracts []kernel.ID) ([]kernel.ID, error)
}

// Auditor — порт журнала аудита (AD-04): изменения дат фиксируются в домене, откуда бы они ни пришли.
type Auditor interface {
	DateChanged(ctx context.Context, actor string, featureID, productID kernel.ID, oldDate, newDate kernel.Date, reason string, affected int) error
}

// Service — публичный интерфейс модуля. Граф держится в памяти и синхронизируется с хранилищем.
type Service struct {
	mu          sync.RWMutex
	g           *Graph
	store       Store
	pub         kernel.Publisher
	clock       kernel.Clock
	commitments CommitmentChecker
	auditor     Auditor
	rollup      map[kernel.ID]FeatureValue
	dirty       bool // rollup устарел; пересчитывается лениво при чтении или явно (NF-P04)
	loaded      bool
}

// NewService создаёт сервис. Граф загружается при первом обращении или через Load.
func NewService(store Store, pub kernel.Publisher, clock kernel.Clock) *Service {
	return &Service{g: NewGraph(), store: store, pub: pub, clock: clock}
}

// WithCommitments подключает порт обязательств.
func (s *Service) WithCommitments(c CommitmentChecker) *Service {
	s.commitments = c
	return s
}

// WithAuditor подключает журнал аудита.
func (s *Service) WithAuditor(a Auditor) *Service {
	s.auditor = a
	return s
}

// Load загружает граф из хранилища в память.
func (s *Service) Load(ctx context.Context) error {
	snap, err := s.store.Load(ctx)
	if err != nil {
		return fmt.Errorf("portfoliograph load: %w", err)
	}
	g := NewGraph()
	if snap.Settings.Coefficients != nil {
		g.settings = snap.Settings
	}
	for _, p := range snap.Products {
		g.putProduct(p)
	}
	for _, c := range snap.Capabilities {
		g.putCapability(c)
	}
	for _, f := range snap.Features {
		g.putFeature(f)
	}
	for _, r := range snap.Requirements {
		g.putRequirement(r)
	}
	for _, l := range snap.Links {
		g.putLink(l)
	}
	for _, c := range snap.Contracts {
		g.putContract(c)
	}
	if !g.IsAcyclic() {
		return fmt.Errorf("%w: граф зависимостей фич в хранилище содержит цикл", kernel.ErrConflict)
	}
	values, err := g.Rollup(s.clock.Now())
	if err != nil {
		return fmt.Errorf("rollup: %w", err)
	}
	s.mu.Lock()
	s.g = g
	s.rollup = indexRollup(values)
	s.loaded = true
	s.mu.Unlock()
	return nil
}

func (s *Service) ensureLoaded(ctx context.Context) error {
	s.mu.RLock()
	ok := s.loaded
	s.mu.RUnlock()
	if ok {
		return nil
	}
	return s.Load(ctx)
}

// refresh пересчитывает rollup, если он устарел. Вызывается перед чтением значений.
func (s *Service) refresh(ctx context.Context) error {
	if err := s.ensureLoaded(ctx); err != nil {
		return err
	}
	s.mu.RLock()
	dirty := s.dirty
	s.mu.RUnlock()
	if !dirty {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	return s.recomputeLocked(ctx, "system")
}

func (s *Service) emit(ctx context.Context, typ string, aggregate, product kernel.ID, actor string, payload any) error {
	if s.pub == nil {
		return nil
	}
	ev, err := kernel.NewEvent(s.clock, typ, aggregate, product, actor, payload)
	if err != nil {
		return err
	}
	return s.pub.Publish(ctx, ev)
}

// ---- Продукты (PG-01, PG-06) ----

// ProductInput — данные для создания или изменения продукта.
type ProductInput struct {
	Key            string
	Name           string
	Type           ProductType
	Owner          string
	Lifecycle      Lifecycle
	SSDLCCertified bool
	HubManual      bool
}

func (in ProductInput) validate() error {
	if in.Key == "" {
		return kernel.Invalid("key", "обязателен")
	}
	if in.Name == "" {
		return kernel.Invalid("name", "обязателен")
	}
	switch in.Type {
	case ProductTypeSecurity, ProductTypeInfrastructure, ProductTypePlatform, ProductTypeOther:
	default:
		return kernel.Invalid("type", "неизвестный тип продукта")
	}
	switch in.Lifecycle {
	case "", LifecycleIdea, LifecycleActive, LifecycleSunset, LifecycleRetired:
	default:
		return kernel.Invalid("lifecycle", "неизвестная стадия")
	}
	return nil
}

// CreateProduct создаёт продукт. Требуется право создания в портфеле (CPO или admin).
func (s *Service) CreateProduct(ctx context.Context, sc authz.Scope, in ProductInput) (Product, error) {
	if err := sc.Require(authz.ActionWriteGraph, kernel.NilID); err != nil {
		return Product{}, err
	}
	if err := in.validate(); err != nil {
		return Product{}, err
	}
	if err := s.ensureLoaded(ctx); err != nil {
		return Product{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.g.products {
		if p.Key == in.Key {
			return Product{}, fmt.Errorf("%w: продукт с ключом %q уже есть", kernel.ErrConflict, in.Key)
		}
	}
	now := s.clock.Now()
	p := Product{
		ID: kernel.NewID(), Key: in.Key, Name: in.Name, Type: in.Type, Owner: in.Owner,
		Lifecycle: in.Lifecycle, SSDLCCertified: in.SSDLCCertified, HubManual: in.HubManual, CreatedAt: now, UpdatedAt: now,
	}
	if p.Lifecycle == "" {
		p.Lifecycle = LifecycleActive
	}
	if err := s.store.SaveProduct(ctx, p); err != nil {
		return Product{}, fmt.Errorf("save product: %w", err)
	}
	s.g.putProduct(p)
	if err := s.emit(ctx, EventProductCreated, p.ID, p.ID, sc.Subject(), p); err != nil {
		return Product{}, err
	}
	return p, nil
}

// UpdateProduct изменяет атрибуты продукта.
func (s *Service) UpdateProduct(ctx context.Context, sc authz.Scope, id kernel.ID, in ProductInput) (Product, error) {
	if err := sc.Require(authz.ActionWriteGraph, id); err != nil {
		return Product{}, err
	}
	if err := in.validate(); err != nil {
		return Product{}, err
	}
	if err := s.ensureLoaded(ctx); err != nil {
		return Product{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.g.products[id]
	if !ok {
		return Product{}, kernel.NotFound("product", id)
	}
	// Ручное назначение хаба — только CPO/admin.
	if in.HubManual != p.HubManual && !sc.HasRole(authz.RoleCPO) && !sc.HasRole(authz.RoleAdmin) {
		return Product{}, fmt.Errorf("%w: назначение хаба — только руководитель портфеля", kernel.ErrForbidden)
	}
	upd := *p
	upd.Key, upd.Name, upd.Type, upd.Owner = in.Key, in.Name, in.Type, in.Owner
	if in.Lifecycle != "" {
		upd.Lifecycle = in.Lifecycle
	}
	upd.SSDLCCertified, upd.HubManual = in.SSDLCCertified, in.HubManual
	upd.UpdatedAt = s.clock.Now()
	if err := s.store.SaveProduct(ctx, upd); err != nil {
		return Product{}, fmt.Errorf("save product: %w", err)
	}
	s.g.putProduct(upd)
	return upd, nil
}

// DeleteProduct удаляет продукт с его возможностями, фичами, требованиями и связями (PG-01).
// Продукт, участвующий в контрактах, не удаляется: сначала контракты нужно снять (ErrConflict).
// Требуется право создания в портфеле (CPO или admin) и приватный доступ к продукту.
func (s *Service) DeleteProduct(ctx context.Context, sc authz.Scope, id kernel.ID) error {
	if err := sc.Require(authz.ActionWriteGraph, kernel.NilID); err != nil {
		return err
	}
	if err := sc.Require(authz.ActionReadPrivate, id); err != nil {
		return err
	}
	if err := s.ensureLoaded(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.g.products[id]; !ok {
		return kernel.NotFound("product", id)
	}
	var names []string
	for _, c := range s.g.contracts {
		if c.ProviderProductID == id || c.ConsumerProductID == id {
			names = append(names, c.Name)
		}
	}
	if len(names) > 0 {
		sort.Strings(names)
		return fmt.Errorf("%w: продукт участвует в контрактах: %s", kernel.ErrConflict, strings.Join(names, ", "))
	}
	if err := s.store.DeleteProduct(ctx, id); err != nil {
		return fmt.Errorf("delete product: %w", err)
	}
	s.g.removeProduct(id)
	s.dirty = true
	return s.emit(ctx, EventProductDeleted, id, id, sc.Subject(), map[string]any{"product_id": id})
}

// Products возвращает продукты, видимые субъекту (хотя бы стратегически).
func (s *Service) Products(ctx context.Context, sc authz.Scope) ([]Product, error) {
	if err := s.ensureLoaded(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Product, 0, len(s.g.products))
	for _, p := range s.g.products {
		if sc.Allows(authz.ActionReadStrategic, p.ID) {
			out = append(out, *p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// Product возвращает продукт.
func (s *Service) Product(ctx context.Context, sc authz.Scope, id kernel.ID) (Product, error) {
	if err := sc.Require(authz.ActionReadStrategic, id); err != nil {
		return Product{}, err
	}
	if err := s.ensureLoaded(ctx); err != nil {
		return Product{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.g.products[id]
	if !ok {
		return Product{}, kernel.NotFound("product", id)
	}
	return *p, nil
}

// Hubs — роль хаба по входящей связности и ручному назначению (PG-06).
func (s *Service) Hubs(ctx context.Context, sc authz.Scope) ([]HubInfo, error) {
	if err := s.ensureLoaded(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := s.g.Hubs()
	out := all[:0:0]
	for _, h := range all {
		if sc.Allows(authz.ActionReadStrategic, h.ProductID) {
			out = append(out, h)
		}
	}
	return out, nil
}

// ---- Порты для identityaccess (без Scope: вызываются до его построения) ----

// ProductIDByKey — идентификатор продукта по ключу.
func (s *Service) ProductIDByKey(ctx context.Context, key string) (kernel.ID, error) {
	if err := s.ensureLoaded(ctx); err != nil {
		return kernel.NilID, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.g.products {
		if p.Key == key {
			return p.ID, nil
		}
	}
	return kernel.NilID, fmt.Errorf("%w: продукт %q", kernel.ErrNotFound, key)
}

// LinkedProducts — продукты, связанные ребром графа (PG-10).
func (s *Service) LinkedProducts(ctx context.Context, id kernel.ID) ([]kernel.ID, error) {
	if err := s.ensureLoaded(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.g.LinkedProducts(id), nil
}

// ---- Иерархия (PG-02) ----

// CreateCapability создаёт возможность продукта.
func (s *Service) CreateCapability(ctx context.Context, sc authz.Scope, productID kernel.ID, name string) (Capability, error) {
	if err := sc.Require(authz.ActionWriteGraph, productID); err != nil {
		return Capability{}, err
	}
	if name == "" {
		return Capability{}, kernel.Invalid("name", "обязателен")
	}
	if err := s.ensureLoaded(ctx); err != nil {
		return Capability{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.g.products[productID]; !ok {
		return Capability{}, kernel.NotFound("product", productID)
	}
	c := Capability{ID: kernel.NewID(), ProductID: productID, Name: name}
	if err := s.store.SaveCapability(ctx, c); err != nil {
		return Capability{}, fmt.Errorf("save capability: %w", err)
	}
	s.g.putCapability(c)
	return c, nil
}

// FeatureInput — данные фичи.
type FeatureInput struct {
	CapabilityID kernel.ID
	Name         string
	Status       FeatureStatus
	OwnValue     kernel.Money
	PlannedDate  kernel.Date
	ExternalKey  string
}

// CreateFeature создаёт фичу внутри продукта.
func (s *Service) CreateFeature(ctx context.Context, sc authz.Scope, productID kernel.ID, in FeatureInput) (Feature, error) {
	if err := sc.Require(authz.ActionWriteGraph, productID); err != nil {
		return Feature{}, err
	}
	if in.Name == "" {
		return Feature{}, kernel.Invalid("name", "обязателен")
	}
	if err := s.ensureLoaded(ctx); err != nil {
		return Feature{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.g.products[productID]; !ok {
		return Feature{}, kernel.NotFound("product", productID)
	}
	if in.CapabilityID != kernel.NilID {
		c, ok := s.g.capabilities[in.CapabilityID]
		if !ok || c.ProductID != productID {
			return Feature{}, kernel.Invalid("capability_id", "возможность не принадлежит продукту")
		}
	}
	now := s.clock.Now()
	f := Feature{
		ID: kernel.NewID(), ProductID: productID, CapabilityID: in.CapabilityID, Name: in.Name,
		Status: in.Status, OwnValue: in.OwnValue, PlannedDate: in.PlannedDate, ExternalKey: in.ExternalKey,
		CreatedAt: now, UpdatedAt: now,
	}
	if f.Status == "" {
		f.Status = FeatureIdea
	}
	if err := s.store.SaveFeature(ctx, f); err != nil {
		return Feature{}, fmt.Errorf("save feature: %w", err)
	}
	s.g.putFeature(f)
	s.dirty = true
	if err := s.emit(ctx, EventFeatureCreated, f.ID, f.ProductID, sc.Subject(), f); err != nil {
		return Feature{}, err
	}
	return f, nil
}

// UpdateFeature изменяет фичу. Смена плановой даты идёт через ShiftFeatureDate.
func (s *Service) UpdateFeature(ctx context.Context, sc authz.Scope, id kernel.ID, in FeatureInput) (Feature, error) {
	if err := s.ensureLoaded(ctx); err != nil {
		return Feature{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.g.features[id]
	if !ok {
		return Feature{}, kernel.NotFound("feature", id)
	}
	if err := sc.Require(authz.ActionWriteGraph, f.ProductID); err != nil {
		return Feature{}, err
	}
	upd := *f
	if in.Name != "" {
		upd.Name = in.Name
	}
	if in.Status != "" {
		upd.Status = in.Status
	}
	if in.CapabilityID != kernel.NilID {
		upd.CapabilityID = in.CapabilityID
	}
	if in.ExternalKey != "" {
		upd.ExternalKey = in.ExternalKey
	}
	upd.UpdatedAt = s.clock.Now()
	if err := s.store.SaveFeature(ctx, upd); err != nil {
		return Feature{}, fmt.Errorf("save feature: %w", err)
	}
	s.g.putFeature(upd)
	return upd, nil
}

// SetFeatureOwnValue задаёт собственную ценность фичи (из сигналов, SG-05) и пересчитывает rollup.
func (s *Service) SetFeatureOwnValue(ctx context.Context, sc authz.Scope, id kernel.ID, v kernel.Money) error {
	if err := s.ensureLoaded(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.g.features[id]
	if !ok {
		return kernel.NotFound("feature", id)
	}
	if err := sc.Require(authz.ActionWriteSignals, f.ProductID); err != nil {
		return err
	}
	upd := *f
	upd.OwnValue = v
	upd.UpdatedAt = s.clock.Now()
	if err := s.store.SaveFeature(ctx, upd); err != nil {
		return fmt.Errorf("save feature: %w", err)
	}
	s.g.putFeature(upd)
	s.dirty = true
	return s.emit(ctx, EventFeatureValueSet, id, f.ProductID, sc.Subject(), upd)
}

// Features возвращает фичи продукта (приватный контур).
func (s *Service) Features(ctx context.Context, sc authz.Scope, productID kernel.ID) ([]Feature, error) {
	if err := sc.Require(authz.ActionReadPrivate, productID); err != nil {
		return nil, err
	}
	if err := s.ensureLoaded(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Feature
	for _, f := range s.g.features {
		if f.ProductID == productID {
			out = append(out, *f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// Feature возвращает фичу (приватный контур продукта).
func (s *Service) Feature(ctx context.Context, sc authz.Scope, id kernel.ID) (Feature, error) {
	if err := s.ensureLoaded(ctx); err != nil {
		return Feature{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	f, ok := s.g.features[id]
	if !ok {
		return Feature{}, kernel.NotFound("feature", id)
	}
	if err := sc.Require(authz.ActionReadPrivate, f.ProductID); err != nil {
		return Feature{}, err
	}
	return *f, nil
}

// FeatureProduct возвращает продукт фичи (PG-10).
//
// Принадлежность фичи продукту входит в стратегический срез портфеля (ТЗ 2.4), сама фича —
// нет: содержание и оценки фичи остаются в приватном контуре. Метод нужен модулям, которые
// работают при стратегическом доступе и которым требуется только продукт фичи —
// compliance (CM-06, CM-07, PR-05) и роли presale, владельца хаба по связанному продукту.
func (s *Service) FeatureProduct(ctx context.Context, sc authz.Scope, id kernel.ID) (kernel.ID, error) {
	if err := s.ensureLoaded(ctx); err != nil {
		return kernel.NilID, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	f, ok := s.g.features[id]
	if !ok {
		return kernel.NilID, kernel.NotFound("feature", id)
	}
	if err := sc.Require(authz.ActionReadStrategic, f.ProductID); err != nil {
		return kernel.NilID, err
	}
	return f.ProductID, nil
}

// FeatureByExternalKey ищет фичу по ключу эпика трекера (DL-01). Для сервисных вызовов.
func (s *Service) FeatureByExternalKey(ctx context.Context, sc authz.Scope, key string) (Feature, error) {
	if err := s.ensureLoaded(ctx); err != nil {
		return Feature{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, f := range s.g.features {
		if f.ExternalKey == key {
			if err := sc.Require(authz.ActionReadPrivate, f.ProductID); err != nil {
				return Feature{}, err
			}
			return *f, nil
		}
	}
	return Feature{}, fmt.Errorf("%w: фича с внешним ключом %q", kernel.ErrNotFound, key)
}

// CreateRequirement добавляет требование к фиче.
func (s *Service) CreateRequirement(ctx context.Context, sc authz.Scope, featureID kernel.ID, text string) (Requirement, error) {
	if text == "" {
		return Requirement{}, kernel.Invalid("text", "обязателен")
	}
	if err := s.ensureLoaded(ctx); err != nil {
		return Requirement{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.g.features[featureID]
	if !ok {
		return Requirement{}, kernel.NotFound("feature", featureID)
	}
	if err := sc.Require(authz.ActionWriteGraph, f.ProductID); err != nil {
		return Requirement{}, err
	}
	r := Requirement{ID: kernel.NewID(), ProductID: f.ProductID, FeatureID: featureID, Text: text}
	if err := s.store.SaveRequirement(ctx, r); err != nil {
		return Requirement{}, fmt.Errorf("save requirement: %w", err)
	}
	s.g.putRequirement(r)
	return r, nil
}

// ---- Связи (PG-03, PG-05) ----

// LinkInput — данные связи.
type LinkInput struct {
	Type          LinkType
	FromProductID kernel.ID
	ToProductID   kernel.ID
	FromFeatureID kernel.ID
	ToFeatureID   kernel.ID
	Criticality   Criticality
	ContractID    kernel.ID
}

func validLinkType(t LinkType) bool {
	switch t {
	case LinkIntegration, LinkSharedComponent, LinkCommercial, LinkBundled:
		return true
	}
	return false
}

func validCriticality(c Criticality) bool {
	switch c {
	case CritBlocks, CritAccelerates, CritDesirable:
		return true
	}
	return false
}

// CreateLink создаёт связь. Связь уровня фич проверяется на цикл; при цикле — *CycleError с путём.
// Право записи требуется на продукт-потребитель (From); продукт-поставщик должен быть виден стратегически.
func (s *Service) CreateLink(ctx context.Context, sc authz.Scope, in LinkInput) (Link, error) {
	if !validLinkType(in.Type) {
		return Link{}, kernel.Invalid("type", "неизвестный тип связи")
	}
	if !validCriticality(in.Criticality) {
		return Link{}, kernel.Invalid("criticality", "неизвестная критичность")
	}
	if err := s.ensureLoaded(ctx); err != nil {
		return Link{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	l := Link{
		ID: kernel.NewID(), Type: in.Type, FromProductID: in.FromProductID, ToProductID: in.ToProductID,
		FromFeatureID: in.FromFeatureID, ToFeatureID: in.ToFeatureID, Criticality: in.Criticality,
		ContractID: in.ContractID, CreatedAt: s.clock.Now(),
	}
	if (in.FromFeatureID == kernel.NilID) != (in.ToFeatureID == kernel.NilID) {
		return Link{}, kernel.Invalid("feature_ids", "связь фич требует обе фичи")
	}
	if l.IsFeatureLevel() {
		ff, ok := s.g.features[l.FromFeatureID]
		if !ok {
			return Link{}, kernel.NotFound("feature", l.FromFeatureID)
		}
		tf, ok := s.g.features[l.ToFeatureID]
		if !ok {
			return Link{}, kernel.NotFound("feature", l.ToFeatureID)
		}
		l.FromProductID, l.ToProductID = ff.ProductID, tf.ProductID
	}
	if _, ok := s.g.products[l.FromProductID]; !ok {
		return Link{}, kernel.NotFound("product", l.FromProductID)
	}
	if _, ok := s.g.products[l.ToProductID]; !ok {
		return Link{}, kernel.NotFound("product", l.ToProductID)
	}
	if err := sc.Require(authz.ActionWriteGraph, l.FromProductID); err != nil {
		return Link{}, err
	}
	if err := sc.Require(authz.ActionReadStrategic, l.ToProductID); err != nil {
		return Link{}, err
	}
	if l.ContractID != kernel.NilID {
		if _, ok := s.g.contracts[l.ContractID]; !ok {
			return Link{}, kernel.NotFound("contract", l.ContractID)
		}
	}
	if l.IsFeatureLevel() {
		if path := s.g.FindCyclePath(l.FromFeatureID, l.ToFeatureID); path != nil {
			return Link{}, &CycleError{Path: path}
		}
	}
	if err := s.store.SaveLink(ctx, l); err != nil {
		return Link{}, fmt.Errorf("save link: %w", err)
	}
	s.g.putLink(l)
	if err := s.emit(ctx, EventLinkCreated, l.ID, l.FromProductID, sc.Subject(), l); err != nil {
		return Link{}, err
	}
	if l.IsFeatureLevel() {
		s.dirty = true
	}
	return l, nil
}

// DeleteLink удаляет связь.
func (s *Service) DeleteLink(ctx context.Context, sc authz.Scope, id kernel.ID) error {
	if err := s.ensureLoaded(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.g.links[id]
	if !ok {
		return kernel.NotFound("link", id)
	}
	if err := sc.Require(authz.ActionWriteGraph, l.FromProductID); err != nil {
		return err
	}
	if err := s.store.DeleteLink(ctx, id); err != nil {
		return fmt.Errorf("delete link: %w", err)
	}
	wasFeature := l.IsFeatureLevel()
	product := l.FromProductID
	s.g.removeLink(id)
	if err := s.emit(ctx, EventLinkDeleted, id, product, sc.Subject(), map[string]any{"link_id": id}); err != nil {
		return err
	}
	if wasFeature {
		s.dirty = true
	}
	return nil
}

// Links возвращает связи, у которых хотя бы одна сторона видна субъекту стратегически (PG-09).
func (s *Service) Links(ctx context.Context, sc authz.Scope) ([]Link, error) {
	if err := s.ensureLoaded(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Link, 0, len(s.g.links))
	for _, l := range s.g.links {
		if sc.Allows(authz.ActionReadStrategic, l.FromProductID) || sc.Allows(authz.ActionReadStrategic, l.ToProductID) {
			out = append(out, *l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// ---- Контракты (PG-04) ----

// ContractInput — данные контракта.
type ContractInput struct {
	Name               string
	ProviderProductID  kernel.ID
	ConsumerProductID  kernel.ID
	ProviderFeatureIDs []kernel.ID
	ConsumerFeatureIDs []kernel.ID
	InterfaceVersion   string
	Owner              string
	Status             ContractStatus
	Criticality        Criticality
	Compatibility      []VersionPair
}

// SaveContract создаёт (id пустой) или обновляет контракт. Фичи контракта связываются рёбрами
// «потребитель → поставщик» с критичностью контракта; циклы отклоняются.
func (s *Service) SaveContract(ctx context.Context, sc authz.Scope, id kernel.ID, in ContractInput) (IntegrationContract, error) {
	if in.Name == "" {
		return IntegrationContract{}, kernel.Invalid("name", "обязателен")
	}
	if in.ProviderProductID == in.ConsumerProductID {
		return IntegrationContract{}, kernel.Invalid("products", "поставщик и потребитель должны быть разными продуктами")
	}
	if !validCriticality(in.Criticality) {
		return IntegrationContract{}, kernel.Invalid("criticality", "неизвестная критичность")
	}
	if err := s.ensureLoaded(ctx); err != nil {
		return IntegrationContract{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, pid := range []kernel.ID{in.ProviderProductID, in.ConsumerProductID} {
		if _, ok := s.g.products[pid]; !ok {
			return IntegrationContract{}, kernel.NotFound("product", pid)
		}
	}
	// Право записи — на любой из двух продуктов; вторая сторона видна стратегически.
	if sc.Allows(authz.ActionWriteGraph, in.ConsumerProductID) {
		if err := sc.Require(authz.ActionReadStrategic, in.ProviderProductID); err != nil {
			return IntegrationContract{}, err
		}
	} else if err := sc.Require(authz.ActionWriteGraph, in.ProviderProductID); err != nil {
		return IntegrationContract{}, err
	} else if err := sc.Require(authz.ActionReadStrategic, in.ConsumerProductID); err != nil {
		return IntegrationContract{}, err
	}
	for _, fid := range in.ProviderFeatureIDs {
		f, ok := s.g.features[fid]
		if !ok || f.ProductID != in.ProviderProductID {
			return IntegrationContract{}, kernel.Invalid("provider_feature_ids", "фича не принадлежит продукту-поставщику")
		}
	}
	for _, fid := range in.ConsumerFeatureIDs {
		f, ok := s.g.features[fid]
		if !ok || f.ProductID != in.ConsumerProductID {
			return IntegrationContract{}, kernel.Invalid("consumer_feature_ids", "фича не принадлежит продукту-потребителю")
		}
	}
	now := s.clock.Now()
	c := IntegrationContract{ID: id, CreatedAt: now}
	if id == kernel.NilID {
		c.ID = kernel.NewID()
	} else if old, ok := s.g.contracts[id]; ok {
		c.CreatedAt, c.SignalValue = old.CreatedAt, old.SignalValue
	} else {
		return IntegrationContract{}, kernel.NotFound("contract", id)
	}
	c.Name, c.ProviderProductID, c.ConsumerProductID = in.Name, in.ProviderProductID, in.ConsumerProductID
	c.ProviderFeatureIDs = append([]kernel.ID(nil), in.ProviderFeatureIDs...)
	c.ConsumerFeatureIDs = append([]kernel.ID(nil), in.ConsumerFeatureIDs...)
	c.InterfaceVersion, c.Owner, c.Status, c.Criticality = in.InterfaceVersion, in.Owner, in.Status, in.Criticality
	c.Compatibility = append([]VersionPair(nil), in.Compatibility...)
	c.UpdatedAt = now
	if c.Status == "" {
		c.Status = ContractDraft
	}

	// Рёбра фич контракта: каждый потребитель зависит от каждого поставщика. Проверяем циклы до записи.
	var newLinks []Link
	for _, cf := range c.ConsumerFeatureIDs {
		for _, pf := range c.ProviderFeatureIDs {
			if s.hasFeatureLink(cf, pf) {
				continue
			}
			if path := s.g.FindCyclePath(cf, pf); path != nil {
				return IntegrationContract{}, &CycleError{Path: path}
			}
			newLinks = append(newLinks, Link{
				ID: kernel.NewID(), Type: LinkIntegration, FromProductID: c.ConsumerProductID, ToProductID: c.ProviderProductID,
				FromFeatureID: cf, ToFeatureID: pf, Criticality: c.Criticality, ContractID: c.ID, CreatedAt: now,
			})
		}
	}
	// Связь уровня продуктов для контракта (роль хаба, PG-06).
	if !s.hasProductLink(c.ConsumerProductID, c.ProviderProductID, c.ID) {
		newLinks = append(newLinks, Link{
			ID: kernel.NewID(), Type: LinkIntegration, FromProductID: c.ConsumerProductID, ToProductID: c.ProviderProductID,
			Criticality: c.Criticality, ContractID: c.ID, CreatedAt: now,
		})
	}
	if err := s.store.SaveContract(ctx, c); err != nil {
		return IntegrationContract{}, fmt.Errorf("save contract: %w", err)
	}
	s.g.putContract(c)
	for _, l := range newLinks {
		if err := s.store.SaveLink(ctx, l); err != nil {
			return IntegrationContract{}, fmt.Errorf("save link: %w", err)
		}
		s.g.putLink(l)
	}
	s.dirty = true
	if err := s.emit(ctx, EventContractSaved, c.ID, c.ConsumerProductID, sc.Subject(), c); err != nil {
		return IntegrationContract{}, err
	}
	return c, nil
}

func (s *Service) hasFeatureLink(from, to kernel.ID) bool {
	for _, l := range s.g.links {
		if l.FromFeatureID == from && l.ToFeatureID == to {
			return true
		}
	}
	return false
}

func (s *Service) hasProductLink(from, to, contract kernel.ID) bool {
	for _, l := range s.g.links {
		if !l.IsFeatureLevel() && l.FromProductID == from && l.ToProductID == to && l.ContractID == contract {
			return true
		}
	}
	return false
}

// SetContractSignalValue задаёт ценность контракта из сигналов (SG-05) и пересчитывает rollup.
func (s *Service) SetContractSignalValue(ctx context.Context, sc authz.Scope, id kernel.ID, v kernel.Money) error {
	if err := s.ensureLoaded(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.g.contracts[id]
	if !ok {
		return kernel.NotFound("contract", id)
	}
	if !sc.Allows(authz.ActionWriteSignals, c.ConsumerProductID) && !sc.Allows(authz.ActionWriteSignals, c.ProviderProductID) {
		return kernel.ErrForbidden
	}
	upd := *c
	upd.SignalValue = v
	upd.UpdatedAt = s.clock.Now()
	if err := s.store.SaveContract(ctx, upd); err != nil {
		return fmt.Errorf("save contract: %w", err)
	}
	s.g.putContract(upd)
	s.dirty = true
	return nil
}

// Contracts — контракты, у которых хотя бы одна сторона видна стратегически.
func (s *Service) Contracts(ctx context.Context, sc authz.Scope) ([]IntegrationContract, error) {
	if err := s.ensureLoaded(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]IntegrationContract, 0, len(s.g.contracts))
	for _, c := range s.g.contracts {
		if sc.Allows(authz.ActionReadStrategic, c.ProviderProductID) || sc.Allows(authz.ActionReadStrategic, c.ConsumerProductID) {
			out = append(out, *c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Contract возвращает контракт с рассчитанным сроком готовности.
func (s *Service) Contract(ctx context.Context, sc authz.Scope, id kernel.ID) (IntegrationContract, kernel.Date, error) {
	if err := s.ensureLoaded(ctx); err != nil {
		return IntegrationContract{}, kernel.Date{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.g.contracts[id]
	if !ok {
		return IntegrationContract{}, kernel.Date{}, kernel.NotFound("contract", id)
	}
	if !sc.Allows(authz.ActionReadStrategic, c.ProviderProductID) && !sc.Allows(authz.ActionReadStrategic, c.ConsumerProductID) {
		return IntegrationContract{}, kernel.Date{}, kernel.ErrForbidden
	}
	return *c, s.g.ContractReadyDate(c), nil
}

// ---- Rollup (PG-07) ----

// recomputeLocked пересчитывает rollup под уже взятой блокировкой записи.
func (s *Service) recomputeLocked(ctx context.Context, actor string) error {
	values, err := s.g.Rollup(s.clock.Now())
	if err != nil {
		return fmt.Errorf("rollup: %w", err)
	}
	if err := s.store.SaveRollup(ctx, values); err != nil {
		return fmt.Errorf("save rollup: %w", err)
	}
	s.rollup = indexRollup(values)
	s.dirty = false
	return s.emit(ctx, EventRollupComputed, kernel.NilID, kernel.NilID, actor, map[string]any{"features": len(values)})
}

func indexRollup(values []FeatureValue) map[kernel.ID]FeatureValue {
	m := make(map[kernel.ID]FeatureValue, len(values))
	for _, v := range values {
		m[v.FeatureID] = v
	}
	return m
}

// Recompute пересчитывает rollup явно (например, после смены настроек).
func (s *Service) Recompute(ctx context.Context, sc authz.Scope) error {
	if !sc.Valid() {
		return kernel.ErrForbidden
	}
	if err := s.ensureLoaded(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recomputeLocked(ctx, sc.Subject())
}

// FeatureValues — значения rollup по продукту (стратегический уровень).
func (s *Service) FeatureValues(ctx context.Context, sc authz.Scope, productID kernel.ID) ([]FeatureValue, error) {
	if err := sc.Require(authz.ActionReadStrategic, productID); err != nil {
		return nil, err
	}
	if err := s.refresh(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []FeatureValue
	for _, v := range s.rollup {
		if v.ProductID == productID {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TotalValue.Amount > out[j].TotalValue.Amount })
	return out, nil
}

// FeatureValue — значение rollup одной фичи.
func (s *Service) FeatureValue(ctx context.Context, sc authz.Scope, id kernel.ID) (FeatureValue, error) {
	if err := s.refresh(ctx); err != nil {
		return FeatureValue{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.rollup[id]
	if !ok {
		return FeatureValue{}, kernel.NotFound("feature value", id)
	}
	if err := sc.Require(authz.ActionReadStrategic, v.ProductID); err != nil {
		return FeatureValue{}, err
	}
	return v, nil
}

// Settings возвращает коэффициенты.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	if err := s.ensureLoaded(ctx); err != nil {
		return Settings{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.g.settings, nil
}

// UpdateSettings меняет коэффициенты (admin) и пересчитывает rollup.
func (s *Service) UpdateSettings(ctx context.Context, sc authz.Scope, st Settings) error {
	if err := sc.Require(authz.ActionAdminSettings, kernel.NilID); err != nil {
		return err
	}
	for _, c := range []Criticality{CritBlocks, CritAccelerates, CritDesirable} {
		if _, ok := st.Coefficients[c]; !ok {
			return kernel.Invalid("coefficients", "не задан коэффициент "+string(c))
		}
		if st.Coefficients[c].IsNegative() {
			return kernel.Invalid("coefficients", "коэффициент не может быть отрицательным")
		}
	}
	if err := s.ensureLoaded(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.store.SaveSettings(ctx, st); err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	s.g.settings = st
	s.dirty = true
	return nil
}

// ---- Сдвиг сроков (PG-08) ----

// ShiftFeatureDate меняет плановую дату фичи и распространяет сдвиг на зависимые фичи.
// Причина обязательна (RM-03); история дат ведётся модулем roadmap по событию EventDateShifted.
func (s *Service) ShiftFeatureDate(ctx context.Context, sc authz.Scope, id kernel.ID, newDate kernel.Date, reason string) (ShiftResult, error) {
	if reason == "" {
		return ShiftResult{}, kernel.Invalid("reason", "причина изменения даты обязательна")
	}
	if newDate.IsZero() {
		return ShiftResult{}, kernel.Invalid("planned_date", "дата обязательна")
	}
	if err := s.ensureLoaded(ctx); err != nil {
		return ShiftResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.g.features[id]
	if !ok {
		return ShiftResult{}, kernel.NotFound("feature", id)
	}
	if err := sc.Require(authz.ActionWriteRoadmap, f.ProductID); err != nil {
		return ShiftResult{}, err
	}
	res := ShiftResult{SourceFeature: id, OldDate: f.PlannedDate, NewDate: newDate}
	upd := *f
	upd.PlannedDate = newDate
	upd.Affected, upd.AffectedBy, upd.ImpliedDate = false, kernel.NilID, kernel.Date{}
	upd.UpdatedAt = s.clock.Now()
	if err := s.store.SaveFeature(ctx, upd); err != nil {
		return ShiftResult{}, fmt.Errorf("save feature: %w", err)
	}
	s.g.putFeature(upd)

	later := res.OldDate.IsZero() || newDate.After(res.OldDate)
	if later {
		res.Affected = s.g.PropagateShift(id, newDate)
		for _, a := range res.Affected {
			dep := s.g.features[a.FeatureID]
			u := *dep
			u.Affected, u.AffectedBy, u.ImpliedDate = true, a.ViaFeature, a.ImpliedDate
			u.UpdatedAt = s.clock.Now()
			if err := s.store.SaveFeature(ctx, u); err != nil {
				return ShiftResult{}, fmt.Errorf("save feature: %w", err)
			}
			s.g.putFeature(u)
		}
	}
	touched := map[kernel.ID]bool{id: true}
	for _, a := range res.Affected {
		touched[a.FeatureID] = true
	}
	for _, c := range s.g.contracts {
		for _, fid := range append(append([]kernel.ID{}, c.ProviderFeatureIDs...), c.ConsumerFeatureIDs...) {
			if touched[fid] {
				res.Contracts = append(res.Contracts, c.ID)
				break
			}
		}
	}
	sort.Slice(res.Contracts, func(i, j int) bool { return res.Contracts[i].String() < res.Contracts[j].String() })
	if s.commitments != nil {
		ids, err := s.commitments.AffectedCommitments(ctx, res.Affected, res.Contracts)
		if err != nil {
			return ShiftResult{}, fmt.Errorf("commitments: %w", err)
		}
		res.Commitments = ids
	}
	payload := struct {
		ShiftResult
		Reason string `json:"reason"`
	}{res, reason}
	if err := s.emit(ctx, EventDateShifted, id, f.ProductID, sc.Subject(), payload); err != nil {
		return ShiftResult{}, err
	}
	if s.auditor != nil {
		if err := s.auditor.DateChanged(ctx, sc.Subject(), id, f.ProductID, res.OldDate, res.NewDate, reason, len(res.Affected)); err != nil {
			return ShiftResult{}, fmt.Errorf("audit: %w", err)
		}
	}
	return res, nil
}

// ---- Стратегический срез (PG-10) ----

// StrategicSlice — срез продукта для владельца связанного продукта: ценность, даты, статусы.
func (s *Service) StrategicSlice(ctx context.Context, sc authz.Scope, productID kernel.ID) (StrategicSlice, error) {
	if err := sc.Require(authz.ActionReadStrategic, productID); err != nil {
		return StrategicSlice{}, err
	}
	if err := s.refresh(ctx); err != nil {
		return StrategicSlice{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.g.products[productID]
	if !ok {
		return StrategicSlice{}, kernel.NotFound("product", productID)
	}
	out := StrategicSlice{Product: *p}
	for _, f := range s.g.features {
		if f.ProductID != productID || f.Status == FeatureRejected {
			continue
		}
		sf := StrategicFeature{ID: f.ID, Name: f.Name, Status: f.Status, PlannedDate: f.PlannedDate, Affected: f.Affected}
		if v, ok := s.rollup[f.ID]; ok {
			sf.TotalValue = v.TotalValue
		}
		out.Features = append(out.Features, sf)
	}
	sort.Slice(out.Features, func(i, j int) bool { return out.Features[i].Name < out.Features[j].Name })
	for _, c := range s.g.contracts {
		if c.ProviderProductID == productID || c.ConsumerProductID == productID {
			out.Contracts = append(out.Contracts, *c)
		}
	}
	sort.Slice(out.Contracts, func(i, j int) bool { return out.Contracts[i].Name < out.Contracts[j].Name })
	return out, nil
}

// Now — время сервиса (для тестов и сидов).
func (s *Service) Now() time.Time { return s.clock.Now() }
